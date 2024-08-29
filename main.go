package main

// A simple program demonstrating the spinner component from the Bubbles
// component library.

import (
	"bytes"
	"fmt"
	"grunner/stopwatch"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

type TestState int

const (
	TestStateWaiting TestState = iota
	TestStateRunning
	TestStateSuccess
	TestStateFailure
	TestStateCompileFailure
)

type testInfo struct {
	id        int
	name      string
	resolved  bool
	stopwatch stopwatch.Model
	state     TestState
	err       error
}

type model struct {
	makefileDir string
	directory   string
	spinner     spinner.Model
	testCases   []testInfo
	quitting    bool
	err         error
}

var (
	titleStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).Padding(0, 2)
	spinnerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	grayStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	testStyle    lipgloss.Style
)

func getTestFiles(dir string) ([]string, error) {
	// get all file names in the current directory
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// filter to those that end with .cc
	var testFiles []string
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		if len(file.Name()) < 3 {
			continue
		}
		if file.Name()[len(file.Name())-3:] == ".cc" {
			testFiles = append(testFiles, file.Name())
		}
	}

	return testFiles, nil
}

func initialModel(dir string) model {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	s.Style = spinnerStyle

	testFiles, err := getTestFiles(dir)
	if err != nil {
		return model{spinner: s, err: err}
	}
	if (len(testFiles)) == 0 {
		return model{spinner: s, err: fmt.Errorf("no test files found")}
	}

	makefile, err := findMakefile(dir)
	if err != nil {
		return model{spinner: s, err: err}
	}

	var testCases []testInfo
	var longestName int
	for i, file := range testFiles {
		file = file[:len(file)-3]
		if len(file) > longestName {
			longestName = len(file)
		}
		testCases = append(testCases, testInfo{id: i, name: file, resolved: false, state: TestStateWaiting, stopwatch: stopwatch.NewWithInterval(time.Millisecond * 31)})
	}

	testStyle = lipgloss.NewStyle().Width(longestName).Align(lipgloss.Right)

	return model{makefileDir: filepath.Dir(makefile), directory: dir, spinner: s, testCases: testCases}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, makeDependencies(m.makefileDir))
}

/**
 * Find the closest Makefile to the given directory
 */
func findMakefile(dir string) (string, error) {
	for {
		makefilePath := filepath.Join(dir, "Makefile")
		if _, err := os.Stat(makefilePath); err == nil {
			return makefilePath, nil
		}

		// Get the parent directory
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			// We have reached the root directory
			break
		}

		dir = parentDir
	}

	return "", fmt.Errorf("makefile not found")
}

func makeDependencies(dir string) tea.Cmd {
	return func() tea.Msg {
		e := exec.Command("make", "-C", "kernel")
		e.Dir = dir
		err := e.Run()
		if err != nil {
			return errMsg{err: fmt.Errorf("make error: %w", err)}
		} else {
			return startBuildingTests{}
		}
	}
}

type startBuildingTests struct{}

func buildTestCase(dir string, testCase testInfo) tea.Cmd {
	return func() tea.Msg {
		// run `make {testInfo.name}.test` in the directory
		e := exec.Command("make", testCase.name)
		e.Dir = dir
		err := e.Run()
		// pipe the output to the terminal
		if err != nil {
			return testBuildErr{testCase.id, errMsg{err: fmt.Errorf("compile error: %w", err)}}
		} else {
			return testBuildSuccess(testCase.id)
		}
	}
}

type testBuildErr struct {
	int
	errMsg
}
type testBuildSuccess int

func runTestCase(dir string, testCase testInfo) tea.Cmd {
	return func() tea.Msg {
		e := exec.Command("make", "-s", fmt.Sprintf("%s.test", testCase.name))
		e.Dir = dir

		var output bytes.Buffer
		e.Stdout = &output

		err := e.Run()
		// pipe the output to the terminal
		if err != nil {
			return testRunError{testCase.id, errMsg{err: fmt.Errorf("failed: %w", err)}}
		} else if strings.Contains(output.String(), "fail") {
			return testRunError{testCase.id, errMsg{err: fmt.Errorf("failed test")}}
		} else {
			return testRunSuccess(testCase.id)
		}
	}
}

type testRunError struct {
	int
	errMsg
}

type testRunSuccess int

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		default:
			return m, nil
		}

	case errMsg:
		m.err = msg
		return m, nil

	case startBuildingTests:
		for i := range m.testCases {
			cmds = append(cmds, buildTestCase(m.makefileDir, m.testCases[i]))
		}
	case testBuildErr:
		m.testCases[msg.int].state = TestStateCompileFailure
		m.testCases[msg.int].resolved = true
		m.testCases[msg.int].err = msg.err
	case testBuildSuccess:
		test := &m.testCases[msg]
		test.state = TestStateRunning
		cmds = append(cmds, test.stopwatch.Start())
		cmds = append(cmds, runTestCase(m.makefileDir, *test))

	case testRunError:
		test := &m.testCases[msg.int]
		test.state = TestStateFailure
		test.resolved = true
		test.err = msg.err
		cmds = append(cmds, test.stopwatch.Stop())
	case testRunSuccess:
		test := &m.testCases[msg]
		test.state = TestStateSuccess
		test.resolved = true
		cmds = append(cmds, test.stopwatch.Stop())
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
		cmds = append(cmds, tea.Quit)
	}

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if m.err != nil {
		return errorStyle.Render("Error: " + m.err.Error())
	}

	str := titleStyle.Render("Test cases running...") + "\n\n"

	//l := list.New().Enumerator(func(items list.Items, i int) string {
	//	return fmt.Sprintf("%s ", m.testCases[i].name)
	//}).
	//	EnumeratorStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("7")).MarginRight(1)).
	//	ItemStyle(lipgloss.NewStyle().UnsetMargins())
	for _, testCase := range m.testCases {
		var (
			icon          string
			statusText    string
			showStopwatch = true
			tError        = ""
		)

		switch testCase.state {
		case TestStateWaiting:
			showStopwatch = false
			icon = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Render("•")
			str += fmt.Sprintf("%s %s compiling...\n", icon, testStyle.Render(testCase.name))
			continue
		case TestStateRunning:
			icon = m.spinner.View()
			statusText = "running..."
			//str += fmt.Sprintf("%s Test %s running... \x1b[90m[%s]\x1b[0m\n", icon, testCase.name, testCase.stopwatch.View())
		case TestStateSuccess:
			icon = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("✔")
			statusText = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true).Render("passed!")
			//str += fmt.Sprintf("%s Test %s passed! \x1b[90m[%s]\x1b[0m\n", icon, testCase.name, testCase.stopwatch.View())
		case TestStateFailure:
			icon = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("✘")
			statusText = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true).Render("failed!")
			//tError = testCase.err.Error()
			//str += fmt.Sprintf("%s Test %s failed! \x1b[90m[%s]\x1b[0m\n %s", icon, testCase.name, testCase.stopwatch.View(), errorStyle.Render(tError))
		case TestStateCompileFailure:
			icon = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("-")
			//tError = testCase.err.Error()
			str += fmt.Sprintf("%s \x1b[37m%s did not compile.\x1b[0m %s\n", icon, testStyle.Render(testCase.name), grayStyle.Render(tError))
			continue
		}

		if showStopwatch {
			str += fmt.Sprintf("%s %s %s \x1b[90m[%s]\x1b[0m %s\n", icon, testStyle.Render(testCase.name), statusText, testCase.stopwatch.View(), errorStyle.Render(tError))
		} else {
			str += fmt.Sprintf("%s %s %s %s\n", icon, testStyle.Render(testCase.name), statusText, errorStyle.Render(tError))
		}
	}

	str += "\n\n"

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
	str += fmt.Sprintf("%d/%d test cases passed.\n", passed, compiled)

	if m.quitting {
		return str + "Terminated.\n"
	}
	return str
}

func main() {
	//dir := flag.String("dir", ".", "directory to search for test files")
	//flag.Parse()
	// get first argument as directory
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	p := tea.NewProgram(initialModel(dir))
	if _, err := p.Run(); err != nil {
		fmt.Println(errorStyle.Render(err.Error()))
		os.Exit(1)
	}
}
