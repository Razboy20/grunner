package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"time"
)

func getTestFiles(dir string) ([]string, error) {
	// check if the directory is actually a file

	//if fileInfo, err := os.Stat(dir); err == nil {
	//	fileInfo
	//}

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
