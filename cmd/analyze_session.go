package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"vortexify-cli/internal/api"
)

func analysisSession(cmd *cobra.Command, execute func(string) error) error {
	out := cmd.ErrOrStderr()
	fmt.Fprintln(out, "Read-only analysis SQL. End a query with ; or use \\g to submit, \\r to clear, \\q to quit.")
	scanner := bufio.NewScanner(cmd.InOrStdin())
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var query strings.Builder
	for {
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		if query.Len() == 0 {
			fmt.Fprint(out, "vortexify=> ")
		} else {
			fmt.Fprint(out, "vortexify-> ")
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			if strings.TrimSpace(query.String()) != "" {
				return fmt.Errorf("unfinished SQL query; end with ; or submit with \\g")
			}
			return nil
		}
		line := scanner.Text()
		submit := false
		// Meta commands are recognized only outside SQL quotes and comments.
		if _, open := sqlQueryState(query.String()); !open {
			switch strings.TrimSpace(line) {
			case `\q`:
				return nil
			case `\r`:
				query.Reset()
				continue
			case `\g`:
				submit = true
			case `\?`:
				fmt.Fprintln(out, `; or \g: submit query, \r: clear query, \q: quit`)
				continue
			}
		}
		if !submit {
			if query.Len() == 0 && strings.TrimSpace(line) == "" {
				continue
			}
			query.WriteString(line)
			query.WriteByte('\n')
		}
		if utf8.RuneCountInString(query.String()) > 32768 {
			return fmt.Errorf("SQL query exceeds 32768 characters")
		}
		complete, _ := sqlQueryState(query.String())
		if !submit && !complete {
			continue
		}
		sql := strings.TrimSpace(query.String())
		query.Reset()
		if sql == "" {
			continue
		}
		if err := execute(sql); err != nil {
			if cmd.Context().Err() != nil {
				return cmd.Context().Err()
			}
			var response *api.APIError
			if errors.As(err, &response) && (response.StatusCode == 401 || response.StatusCode == 403) {
				return err
			}
			fmt.Fprintf(out, "Analysis failed: %v\n", err)
		}
	}
}

// This only detects prompt boundaries; SQL parsing and read-only enforcement
// remain on the server. Semicolons in quoted text/comments are not terminators.
func sqlQueryState(sql string) (complete, open bool) {
	var quote byte
	escapeString := false
	var dollar string
	block := 0
	lineComment := false
	var last byte
	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		if lineComment {
			if ch == '\n' {
				lineComment = false
			}
			continue
		}
		if block > 0 {
			if strings.HasPrefix(sql[i:], "/*") {
				block++
				i++
			} else if strings.HasPrefix(sql[i:], "*/") {
				block--
				i++
			}
			continue
		}
		if dollar != "" {
			if strings.HasPrefix(sql[i:], dollar) {
				i += len(dollar) - 1
				dollar = ""
			}
			continue
		}
		if quote != 0 {
			if escapeString && ch == '\\' && i+1 < len(sql) {
				i++
				continue
			}
			if ch == quote {
				if i+1 < len(sql) && sql[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if strings.HasPrefix(sql[i:], "--") {
			lineComment = true
			i++
			continue
		}
		if strings.HasPrefix(sql[i:], "/*") {
			block++
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			escapeString = ch == '\'' && i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e')
			last = ch
			continue
		}
		if ch == '$' && (i == 0 || !sqlIdentifierByte(sql[i-1])) {
			if end := strings.IndexByte(sql[i+1:], '$'); end >= 0 {
				tag := sql[i+1 : i+1+end]
				valid := true
				for j, c := range tag {
					if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 0 && c >= '0' && c <= '9') {
						valid = false
						break
					}
				}
				if valid {
					dollar = sql[i : i+end+2]
					i += len(dollar) - 1
					last = '$'
					continue
				}
			}
		}
		if !strings.ContainsRune(" \t\r\n", rune(ch)) {
			last = ch
		}
	}
	open = quote != 0 || dollar != "" || block > 0
	return last == ';' && !open, open
}

func sqlIdentifierByte(ch byte) bool {
	return ch == '_' || ch == '$' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}
