package startosis_subpackage_test

import (
	"context"
	"github.com/stretchr/testify/require"
	"strings"
)

const (
	remotePackage        = "github.com/kurtosis-tech/awesome-kurtosis/quickstart"
	expectedOutputLength = 4
	expectedServiceName  = "postgres"

	packageWithSiblingImport = "github.com/h4ck3rk3y/package-panic/primary"
)

func (suite *StartosisSubpackageTestSuite) TestStarlarkRemotePackage() {
	ctx := context.Background()
	isRemotePackage := true
	runResult, err := suite.RunPackage(ctx, remotePackage, isRemotePackage)

	t := suite.T()
	require.NoError(t, err, "Unexpected error executing starlark package")

	require.Nil(t, runResult.InterpretationError, "Unexpected interpretation error")
	require.Empty(t, runResult.ValidationErrors, "Unexpected validation error")
	require.Empty(t, runResult.ExecutionError, "Unexpected execution error")

	runOutputTrimmedString := strings.Trim(string(runResult.RunOutput), "\n")
	runOutputList := strings.Split(runOutputTrimmedString, "\n")

	require.Equal(t, expectedOutputLength, len(runOutputList))
	require.Contains(t, runOutputTrimmedString, expectedServiceName)
}

func (suite *StartosisSubpackageTestSuite) TestStartosisSiblingRemotePackages_RelativeImports() {
	ctx := context.Background()
	isRemotePackage := true
	runResult, _ := suite.RunPackage(ctx, packageWithSiblingImport, isRemotePackage)

	t := suite.T()
	require.Nil(t, runResult.InterpretationError)
	require.Empty(t, runResult.ValidationErrors)
	require.Nil(t, runResult.ExecutionError)

	//expectedResult := "Package loaded.\n"
	//require.Regexp(t, expectedResult, string(runResult.RunOutput))
}
