package main

import (
	"bufio"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var TEST_EXT = ".cc"

type testFile = struct {
	filePath string
	testName string
}

/*
 * Returns a slice of unique test files given from either given 1) directories or 2) files.
 * 1) If a directory is given, searches within the directory for test files.
 * 2) If a file is given, checks if the file exists and if it has a .cc extension (and will attempt to add if not), and adds that.
 */
func findTestFiles(args []string) ([]testFile, error) {
	uniqueTests := make(map[string]string)

	for _, arg := range args {
		arg = strings.TrimSpace(arg)

		fileInfo, err := os.Stat(arg)
		if err == nil && fileInfo.IsDir() {
			entries, err := os.ReadDir(arg)
			if err != nil {
				return nil, fmt.Errorf("error reading directory %s: %v", arg, err)
			}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), TEST_EXT) {
					uniqueTests[entry.Name()] = filepath.Join(arg, entry.Name())
				}
			}
		} else {
			if strings.Contains(arg, ".") {
				if _, err := os.Stat(arg); err == nil {
					uniqueTests[filepath.Base(arg)] = arg
				}
			} else {
				ccFile := arg + TEST_EXT
				if _, err := os.Stat(ccFile); err == nil {
					uniqueTests[filepath.Base(ccFile)] = ccFile
				} else if _, err := os.Stat(arg); err == nil {
					uniqueTests[filepath.Base(arg)] = arg
				}
			}
		}
	}

	result := make([]testFile, 0, len(uniqueTests))
	for file := range uniqueTests {
		if strings.HasSuffix(file, TEST_EXT) {
			result = append(result, testFile{
				filePath: uniqueTests[file],
				testName: strings.TrimSuffix(file, TEST_EXT),
			})
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return strings.Compare(result[i].testName, result[j].testName) < 0
	})

	return result, nil
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

func delayCmd(d time.Duration, cmd tea.Cmd) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return cmd()
	})
}

func timeDiff(a, b time.Time) time.Duration {
	if a.After(b) {
		return a.Sub(b)
	}
	return b.Sub(a)
}

// count the number of other users on the same machine, either with a tty or ssh instance
func countOtherUsers() (int, error) {
	cmd := exec.Command("who")
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("error executing 'who' command: %v", err)
	}

	activeUsers := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)

		if len(fields) >= 2 {
			username := fields[0]
			ttyOrPts := fields[1]

			if strings.HasPrefix(ttyOrPts, "tty") || strings.HasPrefix(ttyOrPts, "pts") {
				activeUsers[username] = true
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error scanning 'who' output: %v", err)
	}

	// Check for code server instances
	cmd = exec.Command("ps", "aux")
	output, err = cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("error executing 'ps aux' command: %v", err)
	}

	scanner = bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "sshd:") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				username := fields[0]
				activeUsers[username] = true
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error scanning 'ps aux' output: %v", err)
	}

	// ignore root and the current user
	delete(activeUsers, "root")
	delete(activeUsers, os.Getenv("USER"))

	return len(activeUsers), nil
}

func hashUser() string {
	cmd := exec.Command("id", "-u")
	output, err := cmd.Output()
	if err != nil {
		return "unknown"
	}

	return strings.TrimSpace(string(output))
}
