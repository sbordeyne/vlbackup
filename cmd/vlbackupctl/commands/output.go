package commands

import (
	"encoding/json"
	"fmt"

	"github.com/sbordeyne/vlbackup/pkg/client"
)

// Options carries settings shared by every subcommand, populated from the
// global CLI flags.
type Options struct {
	// Output selects the rendering of a successful response: "text" or "json".
	Output string
}

// timeRange builds a client.TimeRange from the --from/--to flags. The API
// treats "to" as optional (defaulting to now), so it is only set when a value
// was provided.
func timeRange(from, to string) client.TimeRange {
	tr := client.TimeRange{From: from}
	if to != "" {
		tr.To = &to
	}
	return tr
}

// emit renders a successful response. In "json" mode it prints the raw payload
// as indented JSON; otherwise it invokes the text renderer.
func emit(o Options, payload any, text func()) error {
	if o.Output == "json" {
		b, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding response as json: %w", err)
		}
		fmt.Println(string(b))
		return nil
	}
	text()
	return nil
}

// apiError turns a non-2xx response carrying an ErrorResponse body into a Go
// error, falling back to the raw body when the structured error is absent.
func apiError(status int, errResp *client.ErrorResponse, body []byte) error {
	if errResp != nil && errResp.Error != nil {
		return fmt.Errorf("request failed (HTTP %d): %s", status, *errResp.Error)
	}
	if len(body) > 0 {
		return fmt.Errorf("request failed (HTTP %d): %s", status, string(body))
	}
	return fmt.Errorf("request failed with HTTP %d", status)
}
