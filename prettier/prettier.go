// Package prettier clean up JS, JSON and HTML.
package prettier

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
)

func Format(code, filePath string) (string, error) {
	cmd := exec.Command("npx", "prettier", "--stdin-filepath", filePath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("error creating stdin pipe: %w", err)
	}

	err = cmd.Start()
	if err != nil {
		return "", fmt.Errorf("error starting command: %w", err)
	}

	io.WriteString(stdin, code)
	stdin.Close()

	err = cmd.Wait()
	if err != nil {
		return "", fmt.Errorf("error waiting for command: %w, stderr: %s", err, stderr.String())
	}

	return stdout.String(), nil
}
