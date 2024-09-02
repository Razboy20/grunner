package main

// A simple program demonstrating the spinner component from the Bubbles
// component library.

import (
	"context"
	"fmt"
	"grunner/stopwatch"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fred1268/go-clap/clap"
)

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

type model struct {
	spinner      spinner.Model
	smallSpinner spinner.Model
	testCases    []testInfo

	// settings
	maxThreads  int
	timeCap     time.Duration
	makefileDir string
	directory   string

	// tui data
	window    struct{ width, height int }
	quitting  bool
	context   context.Context
	cancelCtx context.CancelFunc
	err       error
}

var (
	titleStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).Padding(0, 2).
			Width(20).Align(lipgloss.Center)
	spinnerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	grayStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	testStyle    lipgloss.Style
)

func initialModel(files []string, maxThreads, iterations int, timeCap float64) model {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	s.Style = spinnerStyle

	s2 := spinner.New()
	s2.Spinner = spinner.Line

	ctx, cancel := context.WithCancel(context.Background())

	model := model{
		spinner:      s,
		smallSpinner: s2,

		maxThreads: maxThreads,
		timeCap:    time.Duration(timeCap) * time.Second,

		context:   ctx,
		cancelCtx: cancel,
		window:    struct{ width, height int }{80, 24}, // set some defaults
	}

	testFiles, err := findTestFiles(files)
	if err != nil {
		model.err = err
		return model
	}
	if (len(testFiles)) == 0 {
		model.err = fmt.Errorf("no test files found")
		return model
	}

	// assumption is that any tests to run would be within the same Makefile project
	dir := filepath.Dir(testFiles[0])
	makefile, err := findMakefile(dir)
	if err != nil {
		model.err = err
		return model
	}

	var testCases []testInfo
	var longestName int
	for i, file := range testFiles {
		file = file[:len(file)-len(TEST_EXT)]
		if len(file) > longestName {
			longestName = len(file)
		}

		tIterations := make([]testIteration, iterations)
		for i := range tIterations {
			tIterations[i] = testIteration{passed: false, timeSpanned: 0}
		}

		testCases = append(testCases, testInfo{id: i,
			name:       file,
			resolved:   false,
			running:    false,
			state:      TestStateWaiting,
			iterations: tIterations,
			stopwatch:  stopwatch.NewWithInterval(time.Millisecond * 31),
		})
	}

	testStyle = lipgloss.NewStyle().Width(longestName).Align(lipgloss.Right)

	model.testCases = testCases
	model.makefileDir = filepath.Dir(makefile)
	model.directory = dir

	return model
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.smallSpinner.Tick, makeDependencies(m.context, m.makefileDir))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	resolveTestCase := func(test *testInfo) {
		test.resolved = true
		test.running = false

		test.iterations = test.iterations[:test.currIter+1]

		cmds = append(cmds, tryStartExecutors(m))
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			if m.quitting {
				// forcibly quit
				os.Exit(1)
			}
			m.quitting = true
			m.cancelCtx()
			return m, delayCmd(time.Millisecond, tea.Quit)
		default:
			return m, nil
		}

	case errMsg:
		if m.err == nil {
			m.err = msg.err
		}
		m.cancelCtx()
		return m, delayCmd(time.Millisecond, tea.Quit)

	case startBuildingTests:
		cmds = append(cmds, tryStartExecutors(m))
	case buildTestMsg:
		for _, testId := range msg {
			test := &m.testCases[testId]
			test.state = TestStateBuilding
			test.running = true
			cmds = append(cmds, buildTestCase(m.context, m.makefileDir, *test))
		}
	case testBuildErr:
		m.testCases[msg.int].state = TestStateCompileFailure
		resolveTestCase(&m.testCases[msg.int])
		m.testCases[msg.int].err = msg.err
	case testBuildSuccess:
		test := &m.testCases[msg]
		test.state = TestStateRunning
		test.iterations[test.currIter].startTime = time.Now()
		cmds = append(cmds, test.stopwatch.Start())
		cmds = append(cmds, runTestCase(m.context, m.makefileDir, *test))

	case testRunError:
		test := &m.testCases[msg.int]
		test.iterations[test.currIter].passed = false
		test.state = TestStateFailure
		// update timers
		currTime := time.Now()
		test.iterations[test.currIter].timeSpanned = timeDiff(test.iterations[test.currIter].startTime, currTime)
		cmds = append(cmds, test.stopwatch.Stop())

		if test.currIter == len(test.iterations)-1 || test.TimeElapsed() > m.timeCap {
			// all iterations have been run
			resolveTestCase(test)
			test.err = msg.err
			//cmds = append(cmds, test.stopwatch.Stop())
		} else {
			// run the next iteration
			test.currIter++
			test.iterations[test.currIter].startTime = time.Now()
			cmds = append(cmds, runTestCase(m.context, m.makefileDir, *test))
		}
	case testRunSuccess:
		test := &m.testCases[msg]
		test.iterations[test.currIter].passed = true
		// update timers
		currTime := time.Now()
		test.iterations[test.currIter].timeSpanned = timeDiff(test.iterations[test.currIter].startTime, currTime)
		cmds = append(cmds, test.stopwatch.Stop())

		if test.currIter == len(test.iterations)-1 || test.TimeElapsed() > m.timeCap {
			// all iterations have been run
			resolveTestCase(test)
			if test.state != TestStateFailure {
				test.state = TestStateSuccess
			}
		} else {
			// run the next iteration
			test.currIter++
			test.iterations[test.currIter].startTime = time.Now()
			cmds = append(cmds, runTestCase(m.context, m.makefileDir, *test))
		}
	case stopwatch.StartStopMsg:
		for i := range m.testCases {
			testCase := &m.testCases[i]
			testCase.stopwatch, cmd = testCase.stopwatch.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case stopwatch.TickMsg:
		for i := range m.testCases {
			testCase := &m.testCases[i]
			if testCase.stopwatch.ID() == msg.ID {
				testCase.stopwatch, cmd = testCase.stopwatch.Update(msg)
				cmds = append(cmds, cmd)
				break
			}
		}
	case spinner.TickMsg:
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
		m.smallSpinner, cmd = m.smallSpinner.Update(msg)
		cmds = append(cmds, cmd)
	case tea.WindowSizeMsg:
		m.window.width = msg.Width
		m.window.height = msg.Height
	}

	// check if any test cases are still running
	shouldExit := true
	for _, testCase := range m.testCases {
		if !testCase.resolved {
			shouldExit = false
			break
		}
	}

	if shouldExit {
		m.cancelCtx()
		cmds = append(cmds, delayCmd(time.Millisecond, tea.Quit))
	}

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if m.err != nil {
		return errorStyle.Render("Error: " + m.err.Error() + "\n")
	}

	var isFinished bool
	for _, testCase := range m.testCases {
		if !testCase.resolved {
			isFinished = false
			break
		}
		isFinished = true
	}

	isResolved := isFinished || m.quitting

	var str string
	if m.quitting {
		str += titleStyle.Render("Terminated")
	} else if isFinished {
		str += titleStyle.Render("Finished!")
	} else {
		str += titleStyle.Render("Running tests...")
	}

	var passed int
	var compiled int
	for _, testCase := range m.testCases {
		if testCase.state == TestStateSuccess {
			passed++
		}
		if testCase.state != TestStateCompileFailure {
			compiled++
		}
	}

	var titleSpinStr string

	if !isResolved {
		titleSpinStr = m.smallSpinner.View()
	}

	str = lipgloss.JoinHorizontal(lipgloss.Center, str, fmt.Sprintf("  %d/%d test cases passed. %s", passed, compiled, titleSpinStr))

	str += "\n\n"

	var testLines []string
	for _, testCase := range m.testCases {
		testLines = append(testLines, testCase.View(m))
	}

	testStr := strings.Join(testLines, "")

	yPadding := 5
	if height := len(testLines); height > m.window.height-yPadding {
		maxLines := m.window.height - yPadding
		columns := (len(testLines) + maxLines - 1) / maxLines
		columnLines := (len(testLines) + 1) / columns
		// split the test cases into columns
		columnsStrMatrix := make([][]string, columns)
		for i := 0; i < columns; i++ {
			columnsStrMatrix[i] = testLines[i*columnLines : min((i+1)*columnLines, len(testLines)-1)]
		}

		// join the columns
		var columnStrings []string
		for _, column := range columnsStrMatrix {
			columnStrings = append(columnStrings, lipgloss.NewStyle().Width(38).Render(strings.Join(column, "")))
		}

		str += lipgloss.JoinHorizontal(lipgloss.Top, columnStrings...)
	} else {
		str += testStr
	}

	if m.quitting || isFinished {
		return str + "\n"
	}
	return str
}

type argumentConfig struct {
	Iterations int      `clap:"--iterations,-n"`
	TimeCap    float64  `clap:"--timecap,-c"`
	MaxThreads int      `clap:"--threads,-T"`
	ShowHelp   bool     `clap:"--help,-h"`
	TestFiles  []string `clap:"trailing"`
}

func main() {
	//var iterations int
	//var timeCap int
	//var maxThreads int
	//flag.IntVar(&iterations, "n", 1, "number of iterations to execute")
	//flag.IntVar(&timeCap, "timecap", max(runtime.NumCPU()/4-1, 2), "cap total execution time to n seconds (useful with -n)")
	//flag.IntVar(&maxThreads, "threads", max(runtime.NumCPU()/4-1, 2), "maximum number of concurrent threads to use")
	//flag.Parse()
	// get first argument as directory

	if len(os.Args) == 1 {
		printHelp()
		os.Exit(0)
	}

	flags := &argumentConfig{
		Iterations: 1,
		MaxThreads: max(runtime.NumCPU()/4-1, 2),
		TimeCap:    1000,
	}

	var results *clap.Results
	var err error
	if results, err = clap.Parse(os.Args, flags); err != nil {
		fmt.Println(errorStyle.Render("Invalid arguments: " + err.Error()))
		printHelp()
		os.Exit(1)
	}
	if len(results.Ignored) > 1 {
		ignored := results.Ignored[1:]
		if len(ignored) == 1 {
			fmt.Println(errorStyle.Render("Unknown argument: " + ignored[0]))
		} else {
			fmt.Println(errorStyle.Render("Unknown arguments: " + strings.Join(results.Ignored[1:], ", ")))
		}
		printHelp()
		os.Exit(1)
	}

	if flags.ShowHelp {
		printHelp()
		os.Exit(0)
	}

	if flags.TestFiles == nil {
		fmt.Println(errorStyle.Render("No test directory(s) or file(s) given to run."))
		os.Exit(1)
	}

	if flags.MaxThreads > runtime.NumCPU() {
		fmt.Println(errorStyle.Render("WARNING: Number of threads is higher than the number of CPUs. Setting to max CPUs."))
		flags.MaxThreads = runtime.NumCPU()
	} else if flags.MaxThreads > runtime.NumCPU()/4 {
		fmt.Println(errorStyle.Render("WARNING: Number of threads is higher than recommended. You may experience issues."))
	} else if flags.MaxThreads < 1 {
		fmt.Println(errorStyle.Render("Invalid number of threads."))
		os.Exit(1)
	}

	p := tea.NewProgram(initialModel(flags.TestFiles, flags.MaxThreads, flags.Iterations, flags.TimeCap))
	if _, err := p.Run(); err != nil {
		fmt.Println(errorStyle.Render(err.Error()))
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println("Usage: grunner [options] [test files/directories]")
	fmt.Println("Runs test files in the given directories or files. Multiple directories or files can be given.")
	fmt.Println("Options:")
	fmt.Println("  -h, --help             show this help message")
	fmt.Println("  -n, --iterations int   number of iterations to execute (default 1)")
	fmt.Println("  -c, --timecap float    cap total execution time to n seconds (useful with -n) (default 1000)")
	fmt.Println("  -T, --threads int      maximum number of concurrent threads to use (default max(CPUThreads/4-1, 2))")
	fmt.Println("(gRunner version 1.2.2)")
}
