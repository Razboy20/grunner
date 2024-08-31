package main

import (
	"bytes"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"os/exec"
	"strings"
)

type startBuildingTests struct{}
type buildTestMsg []int

func makeDependencies(dir string) tea.Cmd {
	return func() tea.Msg {
		//e := exec.Command("make", "clean")
		//e.Dir = dir
		//err := e.Run()
		//if err != nil {
		//	return errMsg{err: fmt.Errorf("make clean error: %w", err)}
		//}

		var output bytes.Buffer
		e := exec.Command("make", "-C", "kernel")
		e.Dir = dir
		e.Stderr = &output
		err := e.Run()
		if err != nil {
			return errMsg{err: fmt.Errorf("make error: %w\n%s", err,
				lipgloss.NewStyle().
					MarginLeft(2).
					BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
					Render(output.String()))}
		} else {
			return startBuildingTests{}
		}
	}
}

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

func tryStartExecutors(m model) tea.Cmd {
	return func() tea.Msg {
		threadsLeft := m.maxThreads
		var toStart []int

		for i := range m.testCases {
			test := &m.testCases[i]
			if test.state == TestStateWaiting {
				toStart = append(toStart, i)
				threadsLeft--
			}

			if threadsLeft == 0 {
				break
			}
		}

		return buildTestMsg(toStart)
	}
}
