package main

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"grunner/stopwatch"
	"time"
)

type TestState int

const (
	TestStateWaiting TestState = iota
	TestStateBuilding
	TestStateCompileFailure
	TestStateRunning
	TestStateSuccess
	TestStateFailure
)

type testIteration struct {
	passed      bool
	startTime   time.Time
	timeSpanned time.Duration
}

type testInfo struct {
	id       int
	name     string
	filePath string

	running    bool
	resolved   bool
	iterations []testIteration
	currIter   int
	stopwatch  stopwatch.Model
	state      TestState
	err        error
}

func (t testInfo) AverageTime() time.Duration {
	var total time.Duration
	var count int
	for _, iteration := range t.iterations {
		if iteration.timeSpanned > 0 {
			total += iteration.timeSpanned
			count++
		}
	}
	if count == 0 {
		return 0
	} else {
		return (time.Duration(int(total)/count) / time.Millisecond) * time.Millisecond
	}
}

func (t testInfo) TimeElapsed() time.Duration {
	var total time.Duration
	for _, iteration := range t.iterations {
		total += iteration.timeSpanned
	}
	return total
}

func (t testInfo) CountPassed() int {
	var count int
	for _, iteration := range t.iterations {
		if iteration.passed {
			count++
		}
	}
	return count
}

var (
	darkGrayStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	statusStyle   = lipgloss.NewStyle().Width(10)
)

func (t testInfo) View(m model) string {
	var (
		icon         string
		statusText   string
		showMoreInfo = true
		tError       = ""
	)

	switch t.state {
	case TestStateWaiting:
		showMoreInfo = false
		icon = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Render("•")
		return fmt.Sprintf("%s %s waiting...\n", icon, testStyle.Render(t.name))
	case TestStateBuilding:
		showMoreInfo = false
		icon = m.spinner.View()
		return fmt.Sprintf("%s %s compiling...\n", icon, testStyle.Render(t.name))
	case TestStateRunning:
		icon = m.spinner.View()
		statusText = "running..."
	case TestStateSuccess:
		icon = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("✔")
		statusText = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true).Render("passed!")
	case TestStateFailure:
		icon = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("✘")
		statusText = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true).Render("failed!")
		tError = t.err.Error() // todo: make configurable flag
	case TestStateCompileFailure:
		icon = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("-")
		return fmt.Sprintf("%s \x1b[37m%s did not compile.\x1b[0m %s\n", icon, testStyle.Render(t.name), grayStyle.Render(tError))
	}

	if !t.resolved {
		icon = m.spinner.View()
	}

	if showMoreInfo {
		var testCounts string
		if numIterations := len(t.iterations); numIterations > 1 {
			testCounts = darkGrayStyle.Render(fmt.Sprintf("(%d/%d) ", t.CountPassed(), numIterations))
		}
		var shownTime string
		if t.currIter == 0 {
			shownTime = t.stopwatch.View()
		} else {
			shownTime = t.AverageTime().String()
		}

		timeText := darkGrayStyle.Render(fmt.Sprintf("[%s]", shownTime))
		return fmt.Sprintf("%s %s %s %s%s %s\n", icon, testStyle.Render(t.name), statusStyle.Render(statusText), testCounts, timeText, errorStyle.Render(tError))
	} else {
		return fmt.Sprintf("%s %s %s %s\n", icon, testStyle.Render(t.name), statusStyle.Render(statusText), errorStyle.Render(tError))
	}
}
