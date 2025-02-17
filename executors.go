package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/getsentry/sentry-go"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// QemuPath embedded in makefile
var QemuPath string

type startBuildingTests struct{}
type buildTestMsg []int

func makeDependencies(ctx context.Context, dir string) tea.Cmd {
	return func() tea.Msg {
		span := sentry.StartSpan(ctx, "function")
		span.Description = "makeDependencies"
		defer span.Finish()

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		var output bytes.Buffer
		e := exec.CommandContext(ctx, "make", "-C", "kernel")
		e.Dir = dir
		e.Stderr = &output
		err := e.Run()
		if err != nil {
			return errMsg{err: fmt.Errorf("make error: %w\n%s\n\n(Using makefile at: %s)", err,
				lipgloss.NewStyle().
					MarginLeft(2).
					BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
					Render(output.String()), dir)}
		} else {
			return startBuildingTests{}
		}
	}
}

func buildTestCase(ctx context.Context, dir string, testCase testInfo) tea.Cmd {
	return func() tea.Msg {
		span := sentry.StartSpan(ctx, "function")
		span.Description = fmt.Sprintf("build.%d", testCase.id)
		defer span.Finish()

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		_ = os.Remove(fmt.Sprintf("%s.diff", testCase.name))

		// todo: make less janky, and configurable per-project
		// check if Makefile contains .data build steps
		makefileData, _ := os.ReadFile(filepath.Join(dir, "Makefile"))

		var e *exec.Cmd
		if bytes.Contains(makefileData, []byte(".data")) {
			e = exec.CommandContext(ctx, "make", testCase.name, testCase.name+".data")
		} else {
			e = exec.CommandContext(ctx, "make", testCase.name)
		}

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

const ansi = "[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))"

var ansiRe = regexp.MustCompile(ansi)

func runTestCase(m *model, testCase testInfo) tea.Cmd {
	dir := m.makefileDir
	ctx := m.context

	return func() tea.Msg {
		defer sentry.RecoverWithContext(ctx)

		span := sentry.StartSpan(ctx, "function")
		span.Description = fmt.Sprintf("run.%d", testCase.id)
		defer span.Finish()

		ctx, cancel := context.WithTimeout(ctx, m.iterationTimeout)
		defer cancel()

		qemuNumCores, qemuEnvProvided := os.LookupEnv("QEMU_SMP")
		if !qemuEnvProvided {
			qemuNumCores = "4"
		}

		var imageFile string
		// p7+, set to kernel/build/kernel.img
		if _, err := os.Stat(filepath.Join(dir, "kernel/build/kernel.img")); err == nil {
			imageFile = filepath.Join(dir, "kernel/build/kernel.img")
		} else {
			imageFile = filepath.Join(dir, "kernel/build/", testCase.name+".img")
		}

		qemuArgs := fmt.Sprintf("-accel tcg,thread=multi -cpu max -smp %s -m 128m -no-reboot -nographic --monitor none -drive file=%s,index=0,media=disk,format=raw,file.locking=off -device isa-debug-exit,iobase=0xf4,iosize=0x04", qemuNumCores, imageFile)
		if m.verbose {
			qemuArgs += " -d guest_errors"
		}
		// check to see if test.data exists
		dataFile := filepath.Join(dir, testCase.name+".data")
		//return testRunError{testCase.id, errMsg{err: fmt.Errorf(dataFile)}}
		if _, err := os.Stat(dataFile); err == nil {
			qemuArgs += " -drive file=" + dataFile + ",index=1,media=disk,format=file,locking=off"
		}
		qemuCmd := exec.CommandContext(ctx, QemuPath, strings.Fields(qemuArgs)...)
		qemuCmd.Dir = dir

		var output bytes.Buffer
		var stderr bytes.Buffer
		//qemuCmd.Stdout = &output
		qemuCmd.Stderr = &stderr

		rawFile, err := os.Create(fmt.Sprintf("%s.raw", testCase.name))
		if err != nil {
			wrappedErr := fmt.Errorf("failed to create raw file: %w", err)
			sentry.CaptureException(wrappedErr)
			return testRunError{testCase.id, errMsg{err: wrappedErr}}
		}
		defer rawFile.Close()

		stdoutPipe, _ := qemuCmd.StdoutPipe()
		err = qemuCmd.Start()

		if err := ctx.Err(); err != nil {
			return testRunError{testCase.id, errMsg{err: fmt.Errorf("qemu start timed out")}}
		}

		if err != nil {
			wrappedErr := fmt.Errorf("failed to start qemu: %w", err)
			sentry.CaptureException(wrappedErr)
			return testRunError{testCase.id, errMsg{err: wrappedErr}}
		}

		// stream the output to the .raw file
		rawLength, err := io.Copy(io.MultiWriter(rawFile, &output), stdoutPipe)
		if err != nil {
			wrappedErr := fmt.Errorf("failed to write .raw: %w", err)
			sentry.CaptureException(wrappedErr)
			return testRunError{testCase.id, errMsg{err: wrappedErr}}
		}
		if rawLength == 0 {
			return testRunError{testCase.id, errMsg{err: fmt.Errorf(fmt.Sprintf("empty .raw file %s", stderr.String()))}}
		}

		err = qemuCmd.Wait()

		// keep only the lines that start with ***
		lines := strings.Split(output.String(), "\n")
		var newOutput string
		for _, line := range lines {
			line := ansiRe.ReplaceAllString(line, "")
			if strings.HasPrefix(line, "***") {
				newOutput += line + "\n"
			}
		}

		// write the filtered output
		outErr := os.WriteFile(fmt.Sprintf("%s.out", testCase.name), []byte(newOutput), 0644)
		if outErr != nil {
			wrappedErr := fmt.Errorf("failed to write .out: %w", outErr)
			sentry.CaptureException(wrappedErr)
			return testRunError{testCase.id, errMsg{err: wrappedErr}}
		}

		if err := ctx.Err(); err != nil {
			return testRunError{testCase.id, errMsg{err: fmt.Errorf("timed out")}}
		}

		var exitErr2 *exec.ExitError
		if errors.As(err, &exitErr2) {
			if exitErr2.ExitCode() != 1 {
				wrappedErr := fmt.Errorf("qemu failed: %w, %s", err, stderr.String())
				sentry.CaptureException(wrappedErr)
				return testRunError{testCase.id, errMsg{err: wrappedErr}}
			}
		}

		if stderr.Len() > 0 {
			return testRunError{testCase.id, errMsg{err: fmt.Errorf("qemu stderr: %w, %s", err, stderr.String())}}
		}

		// run diff between the output and the .ok file
		var diffOut bytes.Buffer
		diffArgs := fmt.Sprintf("-wBb --color=always - %s", testExtRe.ReplaceAllString(testCase.filePath, ".ok"))
		d := exec.CommandContext(ctx, "diff", strings.Fields(diffArgs)...)
		d.Dir = dir
		d.Stdin = strings.NewReader(newOutput)
		d.Stdout = &diffOut
		diffErr := d.Run()

		if diffErr != nil {
			// store to .diff
			err = os.WriteFile(filepath.Join(dir, testCase.name+".diff"), diffOut.Bytes(), 0644)
			if err != nil {
				return testRunError{testCase.id, errMsg{err: fmt.Errorf("failed to write diff: %w", err)}}
			}

			if strings.Contains(output.String(), "*** Missing code at") {
				return testRunError{testCase.id, errMsg{err: fmt.Errorf("missing code")}}
			}

			return testRunError{testCase.id, errMsg{err: fmt.Errorf("diff found")}}
		} else {
			if testCase.resolved {
				log.Panicf("tried running an already resolved test %s", testCase.name)
			}
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == 1 {
				return testRunSuccess(testCase.id)
			}
			return testRunError{testCase.id, errMsg{err: fmt.Errorf("failed with code %d: %s", exitErr.ExitCode(), exitErr.Stderr)}}
		}
		if err != nil {
			return testRunError{testCase.id, errMsg{err: fmt.Errorf("failed: %w", err)}}
		} else if diffOut.Len() > 0 || strings.Contains(output.String(), "fail") {
			return testRunError{testCase.id, errMsg{err: fmt.Errorf("failed test: %s", output.String())}}
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

		for _, test := range m.testCases {
			if test.running {
				threadsLeft--
			}
		}

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
		// todo: parallelize iterations if nothing else to do

		return buildTestMsg(toStart)
	}
}
