package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

func readInputFile(cmd *cobra.Command, path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(cmd.InOrStdin())
	}
	if path == "" {
		return nil, fmt.Errorf("input file path must not be empty (use - for stdin)")
	}
	return os.ReadFile(path)
}
