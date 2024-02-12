// Code generated by "enumer -type=Verbosity -transform=snake-upper"; DO NOT EDIT.

package run

import (
	"fmt"
	"strings"
)

const _VerbosityName = "BRIEFDETAILEDEXECUTABLEOUTPUT_ONLYDESCRIPTION_ONLY"

var _VerbosityIndex = [...]uint8{0, 5, 13, 23, 34, 50}

const _VerbosityLowerName = "briefdetailedexecutableoutput_onlydescription_only"

func (i Verbosity) String() string {
	if i < 0 || i >= Verbosity(len(_VerbosityIndex)-1) {
		return fmt.Sprintf("Verbosity(%d)", i)
	}
	return _VerbosityName[_VerbosityIndex[i]:_VerbosityIndex[i+1]]
}

// An "invalid array index" compiler error signifies that the constant values have changed.
// Re-run the stringer command to generate them again.
func _VerbosityNoOp() {
	var x [1]struct{}
	_ = x[Brief-(0)]
	_ = x[Detailed-(1)]
	_ = x[Executable-(2)]
	_ = x[OutputOnly-(3)]
	_ = x[DescriptionOnly-(4)]
}

var _VerbosityValues = []Verbosity{Brief, Detailed, Executable, OutputOnly, DescriptionOnly}

var _VerbosityNameToValueMap = map[string]Verbosity{
	_VerbosityName[0:5]:        Brief,
	_VerbosityLowerName[0:5]:   Brief,
	_VerbosityName[5:13]:       Detailed,
	_VerbosityLowerName[5:13]:  Detailed,
	_VerbosityName[13:23]:      Executable,
	_VerbosityLowerName[13:23]: Executable,
	_VerbosityName[23:34]:      OutputOnly,
	_VerbosityLowerName[23:34]: OutputOnly,
	_VerbosityName[34:50]:      DescriptionOnly,
	_VerbosityLowerName[34:50]: DescriptionOnly,
}

var _VerbosityNames = []string{
	_VerbosityName[0:5],
	_VerbosityName[5:13],
	_VerbosityName[13:23],
	_VerbosityName[23:34],
	_VerbosityName[34:50],
}

// VerbosityString retrieves an enum value from the enum constants string name.
// Throws an error if the param is not part of the enum.
func VerbosityString(s string) (Verbosity, error) {
	if val, ok := _VerbosityNameToValueMap[s]; ok {
		return val, nil
	}

	if val, ok := _VerbosityNameToValueMap[strings.ToLower(s)]; ok {
		return val, nil
	}
	return 0, fmt.Errorf("%s does not belong to Verbosity values", s)
}

// VerbosityValues returns all values of the enum
func VerbosityValues() []Verbosity {
	return _VerbosityValues
}

// VerbosityStrings returns a slice of all String values of the enum
func VerbosityStrings() []string {
	strs := make([]string, len(_VerbosityNames))
	copy(strs, _VerbosityNames)
	return strs
}

// IsAVerbosity returns "true" if the value is listed in the enum definition. "false" otherwise
func (i Verbosity) IsAVerbosity() bool {
	for _, v := range _VerbosityValues {
		if i == v {
			return true
		}
	}
	return false
}
