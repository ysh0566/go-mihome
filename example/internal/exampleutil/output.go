package exampleutil

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	miot "github.com/ysh0566/go-mihome"
)

// PrintJSON writes one formatted JSON document followed by a trailing newline.
func PrintJSON[T any](w io.Writer, value T) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// PrintJSONStdout writes one formatted JSON document to stdout.
func PrintJSONStdout[T any](value T) error {
	return PrintJSON(os.Stdout, value)
}

// FormatSpecValue renders a typed MIoT scalar in a readable form for example output.
func FormatSpecValue(value miot.SpecValue) string {
	switch value.Kind() {
	case miot.SpecValueKindBool:
		v, _ := value.Bool()
		return fmt.Sprintf("%t", v)
	case miot.SpecValueKindInt:
		v, _ := value.Int()
		return fmt.Sprintf("%d", v)
	case miot.SpecValueKindFloat:
		v, _ := value.Float()
		return fmt.Sprintf("%g", v)
	case miot.SpecValueKindString:
		v, _ := value.String()
		return v
	default:
		return "null"
	}
}
