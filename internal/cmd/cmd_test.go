package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestVersionCmd(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"version"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("version cmd failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Version:") {
		t.Errorf("expected version output, got %s", output)
	}
}

func TestInitCmd(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := tmpDir + "/gen-config.yaml"

	rootCmd.SetArgs([]string{"init", "-o", outputPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init cmd failed: %v", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("config file not generated at %s", outputPath)
	}
}
