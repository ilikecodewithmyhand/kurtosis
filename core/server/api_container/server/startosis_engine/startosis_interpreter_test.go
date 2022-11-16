package startosis_engine

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_rpc_api_bindings"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/binding_constructors"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/services"
	"github.com/kurtosis-tech/kurtosis/container-engine-lib/lib/backend_interface/objects/service"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/service_network"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/add_service"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/exec"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/remove_service"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/render_templates"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/store_files_from_service"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/upload_files"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/startosis_errors"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/startosis_modules/mock_module_content_provider"
	"github.com/kurtosis-tech/kurtosis/core/server/commons/enclave_data_directory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"path/filepath"
	"testing"
)

var testServiceNetwork service_network.ServiceNetwork = service_network.NewEmptyMockServiceNetwork()

const (
	testContainerImageName = "kurtosistech/example-datastore-server"
)

var (
	defaultEntryPointArgs              []string          = nil
	defaultCmdArgs                     []string          = nil
	defaultEnvVars                     map[string]string = nil
	defaultPrivateIPAddressPlaceholder                   = ""
)

func TestStartosisInterpreter_SimplePrintScript(t *testing.T) {
	testString := "Hello World!"
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	startosisInterpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	interpreter := startosisInterpreter
	script := `
print("` + testString + `")
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 0, len(instructions)) // No kurtosis instruction
	require.Nil(t, interpretationError)

	expectedOutput := testString + `
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_ScriptFailingSingleError(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Starting Startosis script!")

unknownInstruction()
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 0, len(instructions))
	require.Empty(t, scriptOutput)

	expectedError := startosis_errors.NewInterpretationErrorWithCustomMsg(
		[]startosis_errors.CallFrame{
			*startosis_errors.NewCallFrame("undefined: unknownInstruction", startosis_errors.NewScriptPosition(4, 1)),
		},
		"Multiple errors caught interpreting the Startosis script. Listing each of them below.",
	)
	require.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_ScriptFailingMultipleErrors(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Starting Startosis script!")

unknownInstruction()
print(unknownVariable)

unknownInstruction2()
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 0, len(instructions))
	require.Empty(t, scriptOutput)

	expectedError := startosis_errors.NewInterpretationErrorWithCustomMsg(
		[]startosis_errors.CallFrame{
			*startosis_errors.NewCallFrame("undefined: unknownInstruction", startosis_errors.NewScriptPosition(4, 1)),
			*startosis_errors.NewCallFrame("undefined: unknownVariable", startosis_errors.NewScriptPosition(5, 7)),
			*startosis_errors.NewCallFrame("undefined: unknownInstruction2", startosis_errors.NewScriptPosition(7, 1)),
		},
		multipleInterpretationErrorMsg,
	)
	require.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_ScriptFailingSyntaxError(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Starting Startosis script!")

load("otherScript.start") # fails b/c load takes in at least 2 args
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 0, len(instructions))
	require.Empty(t, scriptOutput)

	expectedError := startosis_errors.NewInterpretationErrorFromStacktrace(
		[]startosis_errors.CallFrame{
			*startosis_errors.NewCallFrame("load statement must import at least 1 symbol", startosis_errors.NewScriptPosition(4, 5)),
		},
	)
	require.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_ValidSimpleScriptWithInstruction(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	privateIPAddressPlaceholder := "MAGICAL_PLACEHOLDER_TO_REPLACE"
	script := `
print("Starting Startosis script!")

service_id = "example-datastore-server"
print("Adding service " + service_id)

config = struct(
	image = "` + testContainerImageName + `",
	ports = {
		"grpc": struct(number = 1323, protocol = "TCP")
	},
	private_ip_address_placeholder = "` + privateIPAddressPlaceholder + `"
)
datastore_service = add_service(service_id = service_id, config = config)
print("The grpc port is " + str(datastore_service.ports["grpc"].number))
print("The grpc port protocol is " + datastore_service.ports["grpc"].protocol)
print("The datastore service ip address is " + datastore_service.ip_address)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 1, len(instructions))
	require.Nil(t, interpretationError)

	addServiceInstruction := createSimpleAddServiceInstruction(t, "example-datastore-server", testContainerImageName, 1323, 14, 32, starlarkFilenamePlaceholderAsNotUsed, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, privateIPAddressPlaceholder)
	require.Equal(t, instructions[0], addServiceInstruction)

	expectedOutput := `Starting Startosis script!
Adding service example-datastore-server
The grpc port is 1323
The grpc port protocol is TCP
The datastore service ip address is {{kurtosis:example-datastore-server.ip_address}}
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_ValidSimpleScriptWithInstructionMissingContainerName(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Starting Startosis script!")

service_id = "example-datastore-server"
print("Adding service " + service_id)

config = struct(
	# /!\ /!\ missing container name /!\ /!\
	ports = {
		"grpc": struct(number = 1323, protocol = "TCP")
	}
)
add_service(service_id = service_id, config = config)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 0, len(instructions))
	require.Empty(t, scriptOutput)

	expectedError := startosis_errors.NewInterpretationErrorWithCustomMsg(
		[]startosis_errors.CallFrame{
			*startosis_errors.NewCallFrame("<toplevel>", startosis_errors.NewScriptPosition(13, 12)),
			*startosis_errors.NewCallFrame("add_service", startosis_errors.NewScriptPosition(0, 0)),
		},
		"Evaluation error: Missing value 'image' as element of the struct object 'config'",
	)
	require.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_ValidSimpleScriptWithInstructionTypoInProtocol(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Starting Startosis script!")

service_id = "example-datastore-server"
print("Adding service " + service_id)

config = struct(
	image = "` + testContainerImageName + `",
	ports = {
		"grpc": struct(number = 1323, protocol = "TCPK") # typo in protocol
	}
)
add_service(service_id = service_id, config = config)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 0, len(instructions))
	require.Empty(t, scriptOutput)
	expectedError := startosis_errors.NewInterpretationErrorWithCustomMsg(
		[]startosis_errors.CallFrame{
			*startosis_errors.NewCallFrame("<toplevel>", startosis_errors.NewScriptPosition(13, 12)),
			*startosis_errors.NewCallFrame("add_service", startosis_errors.NewScriptPosition(0, 0)),
		},
		"Evaluation error: Port protocol should be one of TCP, SCTP, UDP",
	)
	require.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_ValidSimpleScriptWithInstructionPortNumberAsString(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Starting Startosis script!")

service_id = "example-datastore-server"
print("Adding service " + service_id)

config = struct(
	image = "` + testContainerImageName + `",
	ports = {
		"grpc": struct(number = "1234", protocol = "TCP") # port number should be an int
	}
)
add_service(service_id = service_id, config = config)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 0, len(instructions))
	require.Empty(t, scriptOutput)
	expectedError := startosis_errors.NewInterpretationErrorWithCustomMsg(
		[]startosis_errors.CallFrame{
			*startosis_errors.NewCallFrame("<toplevel>", startosis_errors.NewScriptPosition(13, 12)),
			*startosis_errors.NewCallFrame("add_service", startosis_errors.NewScriptPosition(0, 0)),
		},
		"Evaluation error: Argument 'number' is expected to be an integer. Got starlark.String",
	)
	require.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_ValidScriptWithMultipleInstructions(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Starting Startosis script!")

service_id = "example-datastore-server"
ports = [1323, 1324, 1325]

def deploy_datastore_services():
    for i in range(len(ports)):
        unique_service_id = service_id + "-" + str(i)
        print("Adding service " + unique_service_id)
        config = struct(
			image = "` + testContainerImageName + `",
			ports = {
				"grpc": struct(
					number = ports[i],
					protocol = "TCP"
				)
			}
		)
        add_service(service_id = unique_service_id, config = config)

deploy_datastore_services()
print("Done!")
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 3, len(instructions))
	require.Nil(t, interpretationError)

	addServiceInstruction0 := createSimpleAddServiceInstruction(t, "example-datastore-server-0", testContainerImageName, 1323, 20, 20, starlarkFilenamePlaceholderAsNotUsed, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)
	addServiceInstruction1 := createSimpleAddServiceInstruction(t, "example-datastore-server-1", testContainerImageName, 1324, 20, 20, starlarkFilenamePlaceholderAsNotUsed, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)
	addServiceInstruction2 := createSimpleAddServiceInstruction(t, "example-datastore-server-2", testContainerImageName, 1325, 20, 20, starlarkFilenamePlaceholderAsNotUsed, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)

	require.Equal(t, instructions[0], addServiceInstruction0)
	require.Equal(t, instructions[1], addServiceInstruction1)
	require.Equal(t, instructions[2], addServiceInstruction2)

	expectedOutput := `Starting Startosis script!
Adding service example-datastore-server-0
Adding service example-datastore-server-1
Adding service example-datastore-server-2
Done!
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_SimpleLoading(t *testing.T) {
	barModulePath := "github.com/foo/bar/lib.star"
	seedModules := map[string]string{
		barModulePath: "a=\"World!\"",
	}
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.BulkAddFileContent(seedModules))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
load("` + barModulePath + `", "a")
print("Hello " + a)
`
	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	assert.Equal(t, 0, len(instructions)) // No kurtosis instruction
	assert.Nil(t, interpretationError)

	expectedOutput := `Hello World!
`
	assert.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_TransitiveLoading(t *testing.T) {
	seedModules := make(map[string]string)
	moduleBar := "github.com/foo/bar/lib.star"
	seedModules[moduleBar] = `a="World!"`
	moduleDooWhichLoadsModuleBar := "github.com/foo/doo/lib.star"
	seedModules[moduleDooWhichLoadsModuleBar] = `load("` + moduleBar + `", "a")
b = "Hello " + a
`
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.BulkAddFileContent(seedModules))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
load("` + moduleDooWhichLoadsModuleBar + `", "b")
print(b)

`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	assert.Equal(t, 0, len(instructions)) // No kurtosis instruction
	assert.Nil(t, interpretationError)

	expectedOutput := `Hello World!
`
	assert.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_FailsOnCycle(t *testing.T) {
	seedModules := make(map[string]string)
	moduleBarLoadsModuleDoo := "github.com/foo/bar/lib.star"
	moduleDooLoadsModuleBar := "github.com/foo/doo/lib.star"
	seedModules[moduleBarLoadsModuleDoo] = `load("` + moduleDooLoadsModuleBar + `", "b")
a = "Hello" + b`
	seedModules[moduleDooLoadsModuleBar] = `load("` + moduleBarLoadsModuleDoo + `", "a")
b = "Hello " + a
`
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.BulkAddFileContent(seedModules))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
load("` + moduleDooLoadsModuleBar + `", "b")
print(b)
`

	_, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	assert.Equal(t, 0, len(instructions)) // No kurtosis instruction
	expectedError := startosis_errors.NewInterpretationErrorWithCustomMsg(
		[]startosis_errors.CallFrame{
			*startosis_errors.NewCallFrame("<toplevel>", startosis_errors.NewScriptPosition(2, 1)),
		},
		"Evaluation error: cannot load %v: cannot load %v: cannot load %v: There is a cycle in the load graph",
		moduleDooLoadsModuleBar,
		moduleBarLoadsModuleDoo,
		moduleDooLoadsModuleBar,
	)
	assert.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_FailsOnNonExistentModule(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	nonExistentModule := "github.com/non/existent/module.star"
	script := `
load("` + nonExistentModule + `", "b")
print(b)
`
	_, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	assert.Equal(t, 0, len(instructions)) // No kurtosis instruction

	expectedError := startosis_errors.NewInterpretationErrorWithCustomMsg(
		[]startosis_errors.CallFrame{
			*startosis_errors.NewCallFrame("<toplevel>", startosis_errors.NewScriptPosition(2, 1)),
		},
		"Evaluation error: cannot load %v: An error occurred while loading the module '%v'",
		nonExistentModule,
		nonExistentModule,
	)
	assert.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_LoadingAValidModuleThatPreviouslyFailedToLoadSucceeds(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	barModulePath := "github.com/foo/bar/lib.star"
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
load("` + barModulePath + `", "a")
print("Hello " + a)
`

	// assert that first load fails
	_, interpretationError, _ := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	assert.NotNil(t, interpretationError)

	barModuleContents := "a=\"World!\""
	require.Nil(t, moduleContentProvider.AddFileContent(barModulePath, barModuleContents))
	expectedOutput := `Hello World!
`
	// assert that second load succeeds
	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	assert.Nil(t, interpretationError)
	assert.Equal(t, 0, len(instructions)) // No kurtosis instruction
	assert.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_ValidSimpleScriptWithImportedStruct(t *testing.T) {
	seedModules := make(map[string]string)
	moduleBar := "github.com/foo/bar/lib.star"
	seedModules[moduleBar] = `
service_id = "example-datastore-server"
print("Constructing config")
config = struct(
	image = "kurtosistech/example-datastore-server",
	ports = {
		"grpc": struct(number = 1323, protocol = "TCP")
	}
)
`
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.BulkAddFileContent(seedModules))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
load("` + moduleBar + `", "service_id", "config")
print("Starting Startosis script!")

print("Adding service " + service_id)
add_service(service_id = service_id, config = config)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 1, len(instructions))
	require.Nil(t, interpretationError)

	addServiceInstruction := createSimpleAddServiceInstruction(t, "example-datastore-server", testContainerImageName, 1323, 6, 12, starlarkFilenamePlaceholderAsNotUsed, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)

	require.Equal(t, instructions[0], addServiceInstruction)

	expectedOutput := `Constructing config
Starting Startosis script!
Adding service example-datastore-server
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_ValidScriptWithFunctionsImportedFromOtherModule(t *testing.T) {
	seedModules := make(map[string]string)
	moduleBar := "github.com/foo/bar/lib.star"
	seedModules[moduleBar] = `
service_id = "example-datastore-server"
ports = [1323, 1324, 1325]

def deploy_datastore_services():
    for i in range(len(ports)):
        unique_service_id = service_id + "-" + str(i)
        print("Adding service " + unique_service_id)
        config = struct(
			image = "kurtosistech/example-datastore-server",
			ports = {
				"grpc": struct(
					number = ports[i],
					protocol = "TCP"
				)
			}
		)
        add_service(service_id = unique_service_id, config = config)
`
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.BulkAddFileContent(seedModules))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
load("` + moduleBar + `", "deploy_datastore_services")
print("Starting Startosis script!")

deploy_datastore_services()
print("Done!")
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 3, len(instructions))
	require.Nil(t, interpretationError)

	addServiceInstruction0 := createSimpleAddServiceInstruction(t, "example-datastore-server-0", testContainerImageName, 1323, 18, 20, moduleBar, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)
	addServiceInstruction1 := createSimpleAddServiceInstruction(t, "example-datastore-server-1", testContainerImageName, 1324, 18, 20, moduleBar, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)
	addServiceInstruction2 := createSimpleAddServiceInstruction(t, "example-datastore-server-2", testContainerImageName, 1325, 18, 20, moduleBar, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)

	require.Equal(t, instructions[0], addServiceInstruction0)
	require.Equal(t, instructions[1], addServiceInstruction1)
	require.Equal(t, instructions[2], addServiceInstruction2)

	expectedOutput := `Starting Startosis script!
Adding service example-datastore-server-0
Adding service example-datastore-server-1
Adding service example-datastore-server-2
Done!
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_AddServiceInOtherModulePopulatesQueue(t *testing.T) {
	seedModules := make(map[string]string)
	moduleBar := "github.com/foo/bar/lib.star"
	seedModules[moduleBar] = `
service_id = "example-datastore-server"
print("Constructing config")
config = struct(
	image = "kurtosistech/example-datastore-server",
	ports = {
		"grpc": struct(number = 1323, protocol = "TCP")
	}
)
print("Adding service " + service_id)
add_service(service_id = service_id, config = config)
`
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.BulkAddFileContent(seedModules))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
load("` + moduleBar + `", "service_id", "config")
print("Starting Startosis script!")
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 1, len(instructions))
	require.Nil(t, interpretationError)

	addServiceInstruction := createSimpleAddServiceInstruction(t, "example-datastore-server", testContainerImageName, 1323, 11, 12, moduleBar, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)

	require.Equal(t, instructions[0], addServiceInstruction)

	expectedOutput := `Constructing config
Adding service example-datastore-server
Starting Startosis script!
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_TestInstructionQueueAndOutputBufferDontHaveDupesInterpretingAnotherScript(t *testing.T) {
	seedModules := make(map[string]string)
	moduleBar := "github.com/foo/bar/lib.star"
	seedModules[moduleBar] = `
service_id = "example-datastore-server"
print("Constructing config")
config = struct(
	image = "kurtosistech/example-datastore-server",
	ports = {
		"grpc": struct(number = 1323, protocol = "TCP")
	}
)
print("Adding service " + service_id)
add_service(service_id = service_id, config = config)
`
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.BulkAddFileContent(seedModules))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	scriptA := `
load("` + moduleBar + `", "service_id", "config")
print("Starting Startosis script!")
`
	addServiceInstructionFromScriptA := createSimpleAddServiceInstruction(t, "example-datastore-server", testContainerImageName, 1323, 11, 12, moduleBar, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)

	expectedOutputFromScriptA := `Constructing config
Adding service example-datastore-server
Starting Startosis script!
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, scriptA, EmptyInputArgs)
	require.Equal(t, 1, len(instructions))
	require.Nil(t, interpretationError)
	require.Equal(t, instructions[0], addServiceInstructionFromScriptA)
	require.Equal(t, expectedOutputFromScriptA, string(scriptOutput))

	scriptB := `
print("Starting Startosis script!")

service_id = "example-datastore-server"
print("Adding service " + service_id)

config = struct(
	image = "kurtosistech/example-datastore-server",
	ports = {
		"grpc": struct(number = 1323, protocol = "TCP")
	}
)
add_service(service_id = service_id, config = config)
`
	addServiceInstructionFromScriptB := createSimpleAddServiceInstruction(t, "example-datastore-server", testContainerImageName, 1323, 13, 12, starlarkFilenamePlaceholderAsNotUsed, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)
	expectedOutputFromScriptB := `Starting Startosis script!
Adding service example-datastore-server
`

	scriptOutput, interpretationError, instructions = interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, scriptB, EmptyInputArgs)
	require.Nil(t, interpretationError)
	require.Equal(t, 1, len(instructions))
	require.Equal(t, instructions[0], addServiceInstructionFromScriptB)
	require.Equal(t, expectedOutputFromScriptB, string(scriptOutput))
}

func TestStartosisInterpreter_AddServiceWithEnvVarsCmdArgsAndEntryPointArgs(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Starting Startosis script!")
service_id = "example-datastore-server"
print("Adding service " + service_id)
store_config = struct(
	image = "kurtosistech/example-datastore-server",
	ports = {
		"grpc": struct(number = 1323, protocol = "TCP")
	}
)
datastore_service = add_service(service_id = service_id, config = store_config)
client_service_id = "example-datastore-client"
print("Adding service " + client_service_id)
client_config = struct(
	image = "kurtosistech/example-datastore-client",
	ports = {
		"grpc": struct(number = 1337, protocol = "TCP")
	},
	entry_point_args = ["--store-port " + str(datastore_service.ports["grpc"].number), "--store-ip " + datastore_service.ip_address],
	cmd_args = ["ping", datastore_service.ip_address],
	env_vars = {"STORE_IP": datastore_service.ip_address}
)
add_service(service_id = client_service_id, config = client_config)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Nil(t, interpretationError)
	require.Equal(t, 2, len(instructions))

	dataSourceAddServiceInstruction := createSimpleAddServiceInstruction(t, "example-datastore-server", testContainerImageName, 1323, 11, 32, starlarkFilenamePlaceholderAsNotUsed, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)

	entryPointArgs := []string{"--store-port 1323", "--store-ip {{kurtosis:example-datastore-server.ip_address}}"}
	cmdArgs := []string{"ping", "{{kurtosis:example-datastore-server.ip_address}}"}
	envVars := map[string]string{"STORE_IP": "{{kurtosis:example-datastore-server.ip_address}}"}
	clientAddServiceInstruction := createSimpleAddServiceInstruction(t, "example-datastore-client", "kurtosistech/example-datastore-client", 1337, 23, 12, starlarkFilenamePlaceholderAsNotUsed, entryPointArgs, cmdArgs, envVars, defaultPrivateIPAddressPlaceholder)

	require.Equal(t, instructions[0], dataSourceAddServiceInstruction)
	require.Equal(t, instructions[1], clientAddServiceInstruction)

	expectedOutput := `Starting Startosis script!
Adding service example-datastore-server
Adding service example-datastore-client
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_ValidExecScriptWithoutExitCodeDefaultsTo0(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Executing mkdir!")
exec(service_id = "example-datastore-server", command = ["mkdir", "/tmp/foo"])
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 1, len(instructions))
	require.Nil(t, interpretationError)

	execInstruction := exec.NewExecInstruction(
		testServiceNetwork,
		*kurtosis_instruction.NewInstructionPosition(3, 5, starlarkFilenamePlaceholderAsNotUsed),
		"example-datastore-server",
		[]string{"mkdir", "/tmp/foo"},
		0,
	)

	require.Equal(t, instructions[0], execInstruction)

	expectedOutput := `Executing mkdir!
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_PassedExitCodeIsInterpretedCorrectly(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Executing mkdir!")
exec(service_id = "example-datastore-server", command = ["mkdir", "/tmp/foo"], expected_exit_code = -7)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 1, len(instructions))
	require.Nil(t, interpretationError)

	execInstruction := exec.NewExecInstruction(
		testServiceNetwork,
		*kurtosis_instruction.NewInstructionPosition(3, 5, starlarkFilenamePlaceholderAsNotUsed),
		"example-datastore-server",
		[]string{"mkdir", "/tmp/foo"},
		-7,
	)

	require.Equal(t, instructions[0], execInstruction)

	expectedOutput := `Executing mkdir!
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_StoreFileFromService(t *testing.T) {
	testArtifactUuid, err := enclave_data_directory.NewFilesArtifactUUID()
	require.Nil(t, err)
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Storing file from service!")
artifact_uuid=store_file_from_service(service_id="example-datastore-server", src_path="/foo/bar", artifact_uuid="` + string(testArtifactUuid) + `")
print(artifact_uuid)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Nil(t, interpretationError)
	require.Equal(t, 1, len(instructions))

	storeInstruction := store_files_from_service.NewStoreFilesFromServiceInstruction(
		testServiceNetwork,
		*kurtosis_instruction.NewInstructionPosition(3, 38, starlarkFilenamePlaceholderAsNotUsed),
		"example-datastore-server",
		"/foo/bar",
		testArtifactUuid,
	)

	require.Equal(t, instructions[0], storeInstruction)

	expectedOutput := fmt.Sprintf(`Storing file from service!
%v
`, testArtifactUuid)
	require.Regexp(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_ReadFileFromGithub(t *testing.T) {
	srcPath := "github.com/foo/bar/static_files/main.txt"
	seed := map[string]string{
		srcPath: "this is a test string",
	}
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.BulkAddFileContent(seed))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Reading file from GitHub!")
file_contents=read_file("` + srcPath + `")
print(file_contents)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Nil(t, interpretationError)
	require.Empty(t, instructions)

	expectedOutput := `Reading file from GitHub!
this is a test string
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_DefineFactAndWait(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreterWithFacts(testServiceNetwork, nil, moduleContentProvider)
	scriptFormatStr := `
define_fact(service_id="%v", fact_name="%v", fact_recipe=struct(method="GET", endpoint="/", port_id="http"))
wait(service_id="%v", fact_name="%v")
`
	serviceId := "service"
	factName := "fact"
	script := fmt.Sprintf(scriptFormatStr, serviceId, factName, serviceId, factName)
	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Nil(t, interpretationError)
	require.NotEmpty(t, instructions)
	require.Empty(t, scriptOutput)
}

func TestStartosisInterpreter_RenderTemplates(t *testing.T) {
	testArtifactUuid, err := enclave_data_directory.NewFilesArtifactUUID()
	require.Nil(t, err)
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Rendering template to disk!")
template_data = {
			"Name" : "Stranger",
			"Answer": 6,
			"Numbers": [1, 2, 3],
			"UnixTimeStamp": 1257894000,
			"LargeFloat": 1231231243.43,
			"Alive": True
}
encoded_json = json.encode(template_data)
data = {
	"/foo/bar/test.txt" : {
		"template": "Hello {{.Name}}. The sum of {{.Numbers}} is {{.Answer}}. My favorite moment in history {{.UnixTimeStamp}}. My favorite number {{.LargeFloat}}. Am I Alive? {{.Alive}}",
		"template_data_json": encoded_json
    }
}
artifact_uuid = render_templates(template_and_data_by_dest_rel_filepath = data, artifact_uuid = "` + string(testArtifactUuid) + `")
print(artifact_uuid)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Nil(t, interpretationError)
	require.Equal(t, 1, len(instructions))

	template := "Hello {{.Name}}. The sum of {{.Numbers}} is {{.Answer}}. My favorite moment in history {{.UnixTimeStamp}}. My favorite number {{.LargeFloat}}. Am I Alive? {{.Alive}}"
	templateData := map[string]interface{}{"Name": "Stranger", "Answer": 6, "Numbers": []int{1, 2, 3}, "UnixTimeStamp": 1257894000, "LargeFloat": 1231231243.43, "Alive": true}
	serializedTemplateData := `{"Alive":true,"Answer":6,"LargeFloat":1.23123124343e+09,"Name":"Stranger","Numbers":[1,2,3],"UnixTimeStamp":1257894000}`
	templateDataAsJson, err := json.Marshal(templateData)
	require.Nil(t, err)
	templateAndData := binding_constructors.NewTemplateAndData(template, string(templateDataAsJson))
	templateAndDataByDestFilepath := map[string]*kurtosis_core_rpc_api_bindings.RenderTemplatesToFilesArtifactArgs_TemplateAndData{
		"/foo/bar/test.txt": templateAndData,
	}

	templateAndDataValues := starlark.NewDict(1)
	fooBarTestValuesValues := starlark.NewDict(2)
	require.Nil(t, fooBarTestValuesValues.SetKey(starlark.String("template"), starlark.String("Hello {{.Name}}. The sum of {{.Numbers}} is {{.Answer}}. My favorite moment in history {{.UnixTimeStamp}}. My favorite number {{.LargeFloat}}. Am I Alive? {{.Alive}}")))
	require.Nil(t, fooBarTestValuesValues.SetKey(starlark.String("template_data_json"), starlark.String(serializedTemplateData)))
	fooBarTestValuesValues.Freeze()
	require.Nil(t, templateAndDataValues.SetKey(starlark.String("/foo/bar/test.txt"), fooBarTestValuesValues))
	templateAndDataValues.Freeze()

	renderInstruction := render_templates.NewRenderTemplatesInstruction(
		testServiceNetwork,
		*kurtosis_instruction.NewInstructionPosition(18, 33, starlarkFilenamePlaceholderAsNotUsed),
		templateAndDataByDestFilepath,
		starlark.StringDict{
			"template_and_data_by_dest_rel_filepath": templateAndDataValues,
			"artifact_uuid":                          starlark.String(testArtifactUuid),
		},
		testArtifactUuid,
	)

	require.Equal(t, renderInstruction, instructions[0])

	expectedOutput := fmt.Sprintf(`Rendering template to disk!
%v
`, testArtifactUuid)
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_ReadTypesFromProtoFileInScript(t *testing.T) {
	typesFilePath := "github.com/kurtosis/module/types.proto"
	typesFileContent := `
syntax = "proto3";
message TestType {
  string greetings = 1;
}
`

	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.AddFileContent(typesFilePath, typesFileContent))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
types = import_types(types_file = "github.com/kurtosis/module/types.proto")
test_type = types.TestType({
    "greetings": "Hello World!"
})
print(test_type)
print(test_type.greetings)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Nil(t, interpretationError)
	require.Empty(t, instructions)

	expectedOutput := `TestType(greetings="Hello World!")
Hello World!
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_ReadTypesFromProtoFile_FailuresWrongArgument(t *testing.T) {
	moduleId := "github.com/kurtosis/module"
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
types = import_types(proto_types_file_bad_argument = "github.com/kurtosis/module/types.proto")
print("Hello world!")
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), moduleId, script, EmptyInputArgs)
	require.Empty(t, scriptOutput)
	require.Empty(t, instructions)

	expectedErrorString := "Evaluation error: Unable to parse arguments of command 'import_types'. It should be a non empty string argument pointing to the fully qualified .proto types file (i.e. \"github.com/kurtosis/module/types.proto\")"
	require.Contains(t, interpretationError.Error(), expectedErrorString)
}

func TestStartosisInterpreter_ReadTypesFromProtoFile_FailuresNoTypesFile(t *testing.T) {
	moduleId := "github.com/kurtosis/module"
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
types = import_types("github.com/kurtosis/module/types.proto")
print("Hello world!")
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), moduleId, script, EmptyInputArgs)
	require.Empty(t, scriptOutput)
	require.Empty(t, instructions)

	expectedError := startosis_errors.NewInterpretationErrorWithCustomMsg(
		[]startosis_errors.CallFrame{
			*startosis_errors.NewCallFrame("<toplevel>", startosis_errors.NewScriptPosition(2, 21)),
			*startosis_errors.NewCallFrame("import_types", startosis_errors.NewScriptPosition(0, 0)),
		},
		"Evaluation error: Unable to load types file github.com/kurtosis/module/types.proto. Is the corresponding type file present in the module?",
	)
	require.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_InjectValidInputArgsToModule(t *testing.T) {
	moduleId := "github.com/kurtosis/module"
	typesFilePath := moduleId + "/types.proto"
	typesFileContent := `
syntax = "proto3";
message ModuleInput {
  string greetings = 1;
}
`

	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.AddFileContent(typesFilePath, typesFileContent))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print(input_args.greetings)
`
	serializedArgs := `{"greetings": "Hello World!"}`
	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), moduleId, script, serializedArgs)
	require.Nil(t, interpretationError)
	require.Equal(t, 0, len(instructions))

	expectedOutput := `Hello World!
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_InjectValidInputArgsToNonModuleScript(t *testing.T) {
	moduleId := "github.com/kurtosis/module"
	typesFilePath := moduleId + "/types.proto"
	typesFileContent := `
syntax = "proto3";
message ModuleInput {
  string greetings = 1;
}
`

	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.AddFileContent(typesFilePath, typesFileContent))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print(input_args.greetings)
`
	serializedArgs := `{"greetings": "Hello World!"}`
	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, serializedArgs)
	require.Equal(t, 0, len(instructions))
	require.Empty(t, scriptOutput)

	expectedError := startosis_errors.NewInterpretationError("Passing parameter to a standalone script is not yet supported in Kurtosis.")
	require.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_InvalidProtoFile(t *testing.T) {
	moduleId := "github.com/kurtosis/module"
	typesFilePath := moduleId + "/types.proto"
	typesFileContent := `
syntax "proto3"; // Missing '=' between 'syntax' and '"proto3"''
message ModuleInput {
  string greetings = 1
}
`

	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.AddFileContent(typesFilePath, typesFileContent))
	absFilePath, err := moduleContentProvider.GetOnDiskAbsoluteFilePath(typesFilePath)
	require.Nil(t, err)
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
def main(input_args):
	print(input_args.greetings)
`
	serializedArgs := `{"greetings": "Hello World!"}`
	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), moduleId, script, serializedArgs)
	require.Equal(t, 0, len(instructions))
	require.Empty(t, scriptOutput)

	expectedErrorMsg := fmt.Sprintf(`A non empty parameter was passed to the module 'github.com/kurtosis/module' but the module doesn't contain a valid 'types.proto' file (it is either absent of invalid). To be able to pass a parameter to a Kurtosis module, please define a 'ModuleInput' type in the module's 'types.proto' file
	Caused by: Unable to compile .proto file 'github.com/kurtosis/module/types.proto' (checked out at '%s'). Proto compiler output was: 
%s:2:8: Expected "=".
`, absFilePath, filepath.Base(absFilePath))
	require.Equal(t, expectedErrorMsg, interpretationError.Error())
}

func TestStartosisInterpreter_InjectValidInvalidInputArgsToModule_InvalidJson(t *testing.T) {
	moduleId := "github.com/kurtosis/module"
	typesFilePath := moduleId + "/types.proto"
	typesFileContent := `
syntax = "proto3";
message ModuleInput {
  string greetings = 1;
}
`

	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.AddFileContent(typesFilePath, typesFileContent))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print(input_args.greetings)
`
	serializedArgs := `"greetings": "Hello World!"` // Invalid JSON
	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), moduleId, script, serializedArgs)
	require.Equal(t, 0, len(instructions))
	require.Empty(t, scriptOutput)

	expectedError := startosis_errors.NewInterpretationError(`Module parameter shape does not fit the module expected input type (module: 'github.com/kurtosis/module'). Parameter was: 
"greetings": "Hello World!"
Error was: 
proto: syntax error (line 1:1): unexpected token "greetings"`)
	require.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_InjectValidInvalidInputArgsToModule_ValidJsonButWrongType(t *testing.T) {
	moduleId := "github.com/kurtosis/module"
	typesFilePath := moduleId + "/types.proto"
	typesFileContent := `
syntax = "proto3";
message ModuleInput {
  string greetings = 1;
}
`

	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	require.Nil(t, moduleContentProvider.AddFileContent(typesFilePath, typesFileContent))
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print(input_args.greetings)
`
	serializedArgs := `{"greetings": 3}` // greeting should be a string here
	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), moduleId, script, serializedArgs)
	require.Equal(t, 0, len(instructions))
	require.Empty(t, scriptOutput)

	expectedError := startosis_errors.NewInterpretationError(`Module parameter shape does not fit the module expected input type (module: 'github.com/kurtosis/module'). Parameter was: 
{"greetings": 3}
Error was: 
proto: (line 1:15): invalid value for string type: 3`)
	require.Equal(t, expectedError, interpretationError)
}

func TestStartosisInterpreter_ThreeLevelNestedInstructionPositionTest(t *testing.T) {
	testArtifactUuid, err := enclave_data_directory.NewFilesArtifactUUID()
	require.Nil(t, err)
	storeFileDefinitionPath := "github.com/kurtosis/store.star"
	storeFileContent := `
def store_for_me():
	print("In the store files instruction")
	artifact_uuid=store_file_from_service(service_id="example-datastore-server", src_path="/foo/bar", artifact_uuid = "` + string(testArtifactUuid) + `")
	return artifact_uuid
`

	moduleThatCallsStoreFile := "github.com/kurtosis/foo.star"
	moduleThatCallsStoreFileContent := `
load("github.com/kurtosis/store.star", "store_for_me")
def call_store_for_me():
	print("In the module that calls store.star")
	return store_for_me()
	`

	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	err = moduleContentProvider.AddFileContent(storeFileDefinitionPath, storeFileContent)
	require.Nil(t, err)

	err = moduleContentProvider.AddFileContent(moduleThatCallsStoreFile, moduleThatCallsStoreFileContent)
	require.Nil(t, err)

	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
load("github.com/kurtosis/foo.star", "call_store_for_me")
uuid = call_store_for_me()
print(uuid)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Nil(t, interpretationError)
	require.Equal(t, 1, len(instructions))

	storeInstruction := store_files_from_service.NewStoreFilesFromServiceInstruction(
		testServiceNetwork,
		*kurtosis_instruction.NewInstructionPosition(4, 39, storeFileDefinitionPath),
		"example-datastore-server",
		"/foo/bar",
		testArtifactUuid,
	)

	require.Equal(t, instructions[0], storeInstruction)

	expectedOutput := fmt.Sprintf(`In the module that calls store.star
In the store files instruction
%v
`, testArtifactUuid)
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_ValidSimpleRemoveService(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Starting Startosis script!")
service_id = "example-datastore-server"
remove_service(service_id=service_id)
print("The service example-datastore-server has been removed")
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Equal(t, 1, len(instructions))
	require.Nil(t, interpretationError)

	removeInstruction := remove_service.NewRemoveServiceInstruction(
		testServiceNetwork,
		*kurtosis_instruction.NewInstructionPosition(4, 15, starlarkFilenamePlaceholderAsNotUsed),
		"example-datastore-server",
	)

	require.Equal(t, instructions[0], removeInstruction)

	expectedOutput := `Starting Startosis script!
The service example-datastore-server has been removed
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}

func TestStartosisInterpreter_UploadGetsInterpretedCorrectly(t *testing.T) {
	filePath := "github.com/kurtosis/module/lib/lib.star"
	artifactUuid, err := enclave_data_directory.NewFilesArtifactUUID()
	require.Nil(t, err)
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	err = moduleContentProvider.AddFileContent(filePath, "fooBar")
	require.Nil(t, err)
	filePathOnDisk, err := moduleContentProvider.GetOnDiskAbsoluteFilePath(filePath)
	require.Nil(t, err)
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `upload_files("` + filePath + `","` + string(artifactUuid) + `")
`
	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Nil(t, interpretationError)
	require.Equal(t, 1, len(instructions))
	require.Empty(t, scriptOutput)

	expectedUploadInstruction := upload_files.NewUploadFilesInstruction(
		*kurtosis_instruction.NewInstructionPosition(1, 13, starlarkFilenamePlaceholderAsNotUsed),
		testServiceNetwork, moduleContentProvider, filePath, filePathOnDisk, artifactUuid,
	)

	require.Equal(t, expectedUploadInstruction, instructions[0])
}

func createSimpleAddServiceInstruction(t *testing.T, serviceId service.ServiceID, imageName string, portNumber uint32, lineNumber int32, colNumber int32, fileName string, entryPointArgs []string, cmdArgs []string, envVars map[string]string, privateIPAddressPlaceholder string) *add_service.AddServiceInstruction {
	serviceConfigStringDict := starlark.StringDict{}
	serviceConfigStringDict["image"] = starlark.String(imageName)

	if portNumber != 0 {
		usedPortDict := starlark.NewDict(1)
		require.Nil(t, usedPortDict.SetKey(
			starlark.String("grpc"),
			starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
				"number":   starlark.MakeInt(int(portNumber)),
				"protocol": starlark.String("TCP"),
			})))
		serviceConfigStringDict["ports"] = usedPortDict
	}

	if entryPointArgs != nil {
		entryPointArgsValues := make([]starlark.Value, 0)
		for _, entryPointArg := range entryPointArgs {
			entryPointArgsValues = append(entryPointArgsValues, starlark.String(entryPointArg))
		}
		serviceConfigStringDict["entry_point_args"] = starlark.NewList(entryPointArgsValues)
	}

	if cmdArgs != nil {
		cmdArgsValues := make([]starlark.Value, 0)
		for _, cmdArg := range cmdArgs {
			cmdArgsValues = append(cmdArgsValues, starlark.String(cmdArg))
		}
		serviceConfigStringDict["cmd_args"] = starlark.NewList(cmdArgsValues)
	}

	if envVars != nil {
		envVarsValues := starlark.NewDict(len(envVars))
		for key, value := range envVars {
			require.Nil(t, envVarsValues.SetKey(starlark.String(key), starlark.String(value)))
		}
		serviceConfigStringDict["env_vars"] = envVarsValues
	}

	if privateIPAddressPlaceholder != "" {
		privateIPAddressPlaceholderStarlarkValue := starlark.String(privateIPAddressPlaceholder)
		serviceConfigStringDict["private_ip_address_placeholder"] = privateIPAddressPlaceholderStarlarkValue
	}

	serviceConfigStruct := starlarkstruct.FromStringDict(starlarkstruct.Default, serviceConfigStringDict)
	serviceConfigStruct.Freeze()

	serviceConfigBuilder := services.NewServiceConfigBuilder(
		imageName,
	)

	if portNumber != 0 {
		serviceConfigBuilder.WithPrivatePorts(
			map[string]*kurtosis_core_rpc_api_bindings.Port{
				"grpc": {
					Number:   portNumber,
					Protocol: kurtosis_core_rpc_api_bindings.Port_TCP,
				},
			},
		)
	}

	if entryPointArgs != nil {
		serviceConfigBuilder.WithEntryPointArgs(entryPointArgs)
	}
	if cmdArgs != nil {
		serviceConfigBuilder.WithCmdArgs(cmdArgs)
	}
	if envVars != nil {
		serviceConfigBuilder.WithEnvVars(envVars)
	}

	if privateIPAddressPlaceholder != "" {
		serviceConfigBuilder.WithPrivateIPAddressPlaceholder(privateIPAddressPlaceholder)
	}

	return add_service.NewAddServiceInstruction(
		testServiceNetwork,
		*kurtosis_instruction.NewInstructionPosition(lineNumber, colNumber, fileName),
		serviceId,
		serviceConfigBuilder.Build(),
		starlark.StringDict{
			"service_id": starlark.String(serviceId),
			"config":     serviceConfigStruct,
		},
	)
}

func TestStartosisInterpreter_NoPanicIfUploadIsPassedAPathNotOnDisk(t *testing.T) {
	filePath := "github.com/kurtosis/module/lib/lib.star"
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `upload_files("` + filePath + `")
`
	_, interpretationError, _ := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.NotNil(t, interpretationError)
}

func TestStartosisInterpreter_NoPortsIsOkayForAddServiceInstruction(t *testing.T) {
	moduleContentProvider := mock_module_content_provider.NewMockModuleContentProvider()
	defer moduleContentProvider.RemoveAll()
	interpreter := NewStartosisInterpreter(testServiceNetwork, moduleContentProvider)
	script := `
print("Starting Startosis script!")

service_id = "example-datastore-server"
print("Adding service " + service_id)

config = struct(
	image = "` + testContainerImageName + `",
)
datastore_service = add_service(service_id = service_id, config = config)
print("The datastore service ip address is " + datastore_service.ip_address)
`

	scriptOutput, interpretationError, instructions := interpreter.Interpret(context.Background(), ModuleIdPlaceholderForStandaloneScripts, script, EmptyInputArgs)
	require.Nil(t, interpretationError)
	require.Equal(t, 1, len(instructions))

	addServiceInstruction := createSimpleAddServiceInstruction(t, "example-datastore-server", testContainerImageName, 0, 10, 32, starlarkFilenamePlaceholderAsNotUsed, defaultEntryPointArgs, defaultCmdArgs, defaultEnvVars, defaultPrivateIPAddressPlaceholder)
	require.Equal(t, instructions[0], addServiceInstruction)

	expectedOutput := `Starting Startosis script!
Adding service example-datastore-server
The datastore service ip address is {{kurtosis:example-datastore-server.ip_address}}
`
	require.Equal(t, expectedOutput, string(scriptOutput))
}
