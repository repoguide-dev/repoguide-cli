package sessionimport

import (
	"reflect"
	"testing"
)

func TestDerivePathsFromShellSkipsSedScript(t *testing.T) {
	_, writes := derivePathsFromShell(`sed -i '' 's/text-gray-600/text-gray-400/g' file.jsx`)
	if !reflect.DeepEqual(writes, []string{"file.jsx"}) {
		t.Fatalf("writes = %v, want [file.jsx]", writes)
	}
}
