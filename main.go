package main

import (
	"context"
	"fmt"
	"grunner/stopwatch"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fred1268/go-clap/clap"
	"github.com/getsentry/sentry-go"
)

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

type model struct {
	spinner      spinner.Model
	smallSpinner spinner.Model
	testCases    []testInfo

	// settings
	maxThreads       int
	timeCap          time.Duration
	iterationTimeout time.Duration
	earlyExit        bool
	verbose          bool

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

// Version embedded in makefile
var Version string
var IsEdge = strings.Contains(os.Args[0], "edge")

func initialModel(ctx context.Context, flags *argumentConfig) model {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	s.Style = spinnerStyle

	s2 := spinner.New()
	s2.Spinner = spinner.Line

	ctx, cancel := context.WithCancel(ctx)

	model := model{
		spinner:      s,
		smallSpinner: s2,

		maxThreads:       flags.MaxThreads,
		timeCap:          time.Duration(flags.TimeCap) * time.Second,
		iterationTimeout: time.Duration(flags.Timeout) * time.Second,
		earlyExit:        flags.EarlyExit,
		verbose:          flags.Verbose,

		context:   ctx,
		cancelCtx: cancel,
		window:    struct{ width, height int }{80, 24}, // set some defaults
	}

	testFiles, err := findTestFiles(flags.TestFiles)
	if err != nil {
		model.err = err
		return model
	}
	if (len(testFiles)) == 0 {
		model.err = fmt.Errorf("no test files found")
		return model
	}

	// assumption is that any tests to run would be within the same Makefile project
	dir := filepath.Dir(testFiles[0].filePath)
	makefile, err := findMakefile(dir)
	if err != nil {
		model.err = err
		return model
	}

	var testCases []testInfo
	var longestName int
	for i, testFile := range testFiles {
		if len(testFile.testName) > longestName {
			longestName = len(testFile.testName)
		}

		tIterations := make([]testIteration, flags.Iterations)
		for i := range tIterations {
			tIterations[i] = testIteration{passed: false, timeSpanned: 0}
		}

		testCases = append(testCases, testInfo{
			id:         i,
			name:       testFile.testName,
			filePath:   testFile.filePath,
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

	if len(testCases) == 1 {
		model.verbose = true
	}

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

	defer sentry.RecoverWithContext(m.context)

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
		cmds = append(cmds, runTestCase(&m, *test))

	case testRunError:
		test := &m.testCases[msg.int]
		test.iterations[test.currIter].passed = false
		test.state = TestStateFailure
		// update timers
		currTime := time.Now()
		test.iterations[test.currIter].timeSpanned = timeDiff(test.iterations[test.currIter].startTime, currTime)
		cmds = append(cmds, test.stopwatch.Stop())

		test.err = msg.err
		if m.earlyExit || test.currIter == len(test.iterations)-1 || (m.timeCap > 0 && test.TimeElapsed() > m.timeCap) {
			// all iterations have been run
			resolveTestCase(test)
		} else {
			// run the next iteration
			test.currIter++
			test.iterations[test.currIter].startTime = time.Now()
			cmds = append(cmds, runTestCase(&m, *test))
		}
	case testRunSuccess:
		test := &m.testCases[msg]
		test.iterations[test.currIter].passed = true
		// update timers
		currTime := time.Now()
		test.iterations[test.currIter].timeSpanned = timeDiff(test.iterations[test.currIter].startTime, currTime)
		cmds = append(cmds, test.stopwatch.Stop())

		if test.currIter == len(test.iterations)-1 || (m.timeCap > 0 && test.TimeElapsed() > m.timeCap) {
			// all iterations have been run
			resolveTestCase(test)
			if test.state != TestStateFailure {
				test.state = TestStateSuccess
			}
		} else {
			// run the next iteration
			test.currIter++
			test.iterations[test.currIter].startTime = time.Now()
			cmds = append(cmds, runTestCase(&m, *test))
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

	defer sentry.RecoverWithContext(m.context)

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
	columnWidth := 38

	if m.verbose {
		columnWidth += 2
	}

	if height := len(testLines); height > m.window.height-yPadding {
		maxLines := m.window.height - yPadding

		if maxLines <= 0 {
			return str
		}

		columns := (len(testLines) + maxLines - 1) / maxLines
		columnLines := (len(testLines) + 1) / columns
		// split the test cases into columns
		columnsStrMatrix := make([][]string, columns)
		for i := 0; i < columns; i++ {
			columnsStrMatrix[i] = testLines[i*columnLines : min((i+1)*columnLines, len(testLines))]
		}

		// join the columns
		var columnStrings []string
		for _, column := range columnsStrMatrix {
			columnStrings = append(columnStrings, lipgloss.NewStyle().Width(columnWidth).Render(strings.Join(column, "")))
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
	MaxThreads int      `clap:"--threads,-T"`
	EarlyExit  bool     `clap:"--earlyexit,-e"`
	TimeCap    float64  `clap:"--timecap,-c"`
	Timeout    int      `clap:"--timeout,-t"`
	ShowHelp   bool     `clap:"--help,-h"`
	Verbose    bool     `clap:"--verbose,-v"`
	TestFiles  []string `clap:"trailing"`
}

func main() {
	exitCode := 0
	defer func() { os.Exit(exitCode) }()
	var sentryEnvironment string
	var sampleRate float64
	if IsEdge {
		sampleRate = 1.0
		sentryEnvironment = "edge"
	} else {
		sampleRate = 0.1
		sentryEnvironment = "production"
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              "https://b84a1ffbb51adf6e332cbca2922b2362@o4507745204895744.ingest.us.sentry.io/4508022556131328",
		EnableTracing:    true,
		Release:          Version,
		AttachStacktrace: true,
		TracesSampleRate: sampleRate,
		Environment:      sentryEnvironment,
	})
	if err != nil {
		log.Fatalf("sentry.Init: %s", err)
	}

	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetUser(sentry.User{Username: hashUser()})
	})

	// Flush buffered events before the program terminates.
	// Set the timeout to the maximum duration the program can afford to wait.
	defer sentry.Flush(2 * time.Second)

	flags := &argumentConfig{
		Iterations: 1,
		EarlyExit:  false, // todo: figure out if a boolean flag can be set to false with clap
		MaxThreads: runtime.NumCPU() / 4,
		TimeCap:    -1,
		Timeout:    10,
		Verbose:    IsEdge,
	}

	var results *clap.Results
	if results, err = clap.Parse(os.Args, flags); err != nil {
		fmt.Println(errorStyle.Render("Invalid arguments: " + err.Error()))
		printHelp()
		exitCode = 1
		return
	}

	if len(os.Args) == 1 || flags.ShowHelp {
		printHelp()
		return
	}

	if len(results.Ignored) > 1 {
		ignored := results.Ignored[1:]
		if len(ignored) == 1 {
			fmt.Println(errorStyle.Render("Unknown argument: " + ignored[0]))
		} else {
			fmt.Println(errorStyle.Render("Unknown arguments: " + strings.Join(results.Ignored[1:], ", ")))
		}
		printHelp()
		exitCode = 1
		return
	}

	if flags.TestFiles == nil {
		fmt.Println(errorStyle.Render("No test directory(s) or file(s) given to run."))
		exitCode = 1
		return
	}

	if flags.MaxThreads > runtime.NumCPU()/4 {
		userCount, err := countOtherUsers()
		// if we can't get the user count, just ignore it
		if err == nil {
			if userCount > 0 {
				pluralUsers := ""
				if userCount > 1 {
					pluralUsers = "s"
				}
				fmt.Println(errorStyle.Render(fmt.Sprintf("WARNING: May incur high CPU usage, be mindful of the %d other user%s on the system.", userCount, pluralUsers)))
			}
		}
	} else if flags.MaxThreads < 1 {
		fmt.Println(errorStyle.Render("Invalid number of threads."))
		return
	}

	options := []sentry.SpanOption{
		// Set the OP based on values from https://develop.sentry.dev/sdk/performance/span-operations/
		sentry.WithOpName("app"),
		sentry.WithTransactionName("testRun"),
		sentry.WithDescription(fmt.Sprintf("Running %d test files", len(flags.TestFiles))),
		sentry.WithTransactionSource(sentry.SourceCustom),
	}

	transaction := sentry.StartSpan(context.Background(), "run", options...)
	defer transaction.Finish()

	model := initialModel(transaction.Context(), flags)

	p := tea.NewProgram(model)

	if _, err := p.Run(); err != nil {
		fmt.Println(errorStyle.Render(err.Error()))
		sentry.CaptureException(err)
		exitCode = 1
		return
	}
}

func printHelp() {
	fmt.Println("Usage: grunner [options] [... test files/directories]")
	fmt.Println("Runs test files in the given directories or files. Multiple directories or files can be given.")
	fmt.Println("\nOptions:")
	fmt.Println("  -h, --help             show this help message")
	fmt.Println("  -n, --iterations int   number of iterations to execute (default 1)")
	fmt.Println("  -T, --threads int      maximum number of concurrent threads to use (default CPUThreads/4)")
	fmt.Println("  -e, --earlyexit        exit iterating early if a test fails")
	fmt.Println("  -t, --timeout int      max time an iteration will run until being killed (default 10)")
	fmt.Println("  -c, --timecap float    cap total execution time to n seconds (useful with -n) (default unlimited)")
	fmt.Println("  -v, --verbose          show error information for test failures")
	fmt.Printf("(gRunner version %s)\n", strings.TrimSpace(Version))
}
