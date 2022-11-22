package output_printers

import (
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/binding_constructors"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFormatInstruction(t *testing.T) {
	instruction := binding_constructors.NewKurtosisInstruction(
		binding_constructors.NewKurtosisInstructionPosition("dummyFile", 12, 4),
		`my_instruction("foo", ["bar", "doo"], kwarg1="serviceA", kwarg2=struct(bonjour=42, hello="world"))`,
		// for now result is appended manually in the exec command code. This is change once we start doing streaming where the result is displayed right after the instruction code
		nil)
	formattedInstruction := FormatInstruction(instruction)
	expectedResult := `# from dummyFile[12:4]
my_instruction(
    "foo",
    [
        "bar",
        "doo",
    ],
    kwarg1 = "serviceA",
    kwarg2 = struct(
        bonjour = 42,
        hello = "world",
    ),
)`
	require.Equal(t, expectedResult, formattedInstruction)
}
