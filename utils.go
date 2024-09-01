package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var TEST_EXT = ".cc"

/*
 * Returns a slice of unique test files given from either given 1) directories or 2) files.
 * 1) If a directory is given, searches within the directory for test files.
 * 2) If a file is given, checks if the file exists and if it has a .cc extension (and will attempt to add if not), and adds that.
 */
func findTestFiles(args []string) ([]string, error) {
	uniqueTests := make(map[string]bool)

	for _, arg := range args {
		fileInfo, err := os.Stat(arg)
		if err == nil && fileInfo.IsDir() {
			entries, err := os.ReadDir(arg)
			if err != nil {
				return nil, fmt.Errorf("error reading directory %s: %v", arg, err)
			}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), TEST_EXT) {
					uniqueTests[entry.Name()] = true
				}
			}
		} else {
			if strings.Contains(arg, ".") {
				if _, err := os.Stat(arg); err == nil {
					uniqueTests[filepath.Base(arg)] = true
				}
			} else {
				ccFile := arg + TEST_EXT
				if _, err := os.Stat(ccFile); err == nil {
					uniqueTests[filepath.Base(ccFile)] = true
				} else if _, err := os.Stat(arg); err == nil {
					uniqueTests[filepath.Base(arg)] = true
				}
			}
		}
	}

	result := make([]string, 0, len(uniqueTests))
	for file := range uniqueTests {
		result = append(result, file)
	}

	sort.Strings(result)
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
