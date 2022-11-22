package add_service

import (
	"github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_rpc_api_bindings"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/services"
	"github.com/kurtosis-tech/kurtosis/container-engine-lib/lib/backend_interface/objects/service"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/service_network"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"net"
	"testing"
)

const (
	testContainerImageName = "kurtosistech/example-datastore-server"
)

func TestAddServiceInstruction_GetCanonicalizedInstruction(t *testing.T) {
	serviceConfigDict := starlark.StringDict{}
	serviceConfigDict["image"] = starlark.String(testContainerImageName)

	usedPortsDict := starlark.NewDict(1)
	port1Dict := starlark.StringDict{}
	port1Dict["number"] = starlark.MakeInt(1234)
	port1Dict["protocol"] = starlark.String("TCP")
	require.Nil(t, usedPortsDict.SetKey(starlark.String("grpc"), starlarkstruct.FromStringDict(starlarkstruct.Default, port1Dict)))
	serviceConfigDict["ports"] = usedPortsDict

	serviceConfigDict["entrypoint"] = starlark.NewList([]starlark.Value{starlark.String("127.0.0.0"), starlark.MakeInt(1234)})

	serviceConfigDict["cmd"] = starlark.NewList([]starlark.Value{starlark.String("bash"), starlark.String("-c"), starlark.String("/apps/main.py"), starlark.MakeInt(1234)})

	envVar := starlark.NewDict(2)
	require.Nil(t, envVar.SetKey(starlark.String("VAR_1"), starlark.String("VALUE_1")))
	require.Nil(t, envVar.SetKey(starlark.String("VAR_2"), starlark.String("VALUE_2")))
	serviceConfigDict["env_vars"] = envVar

	fileArtifactMountDirPath := starlark.NewDict(2)
	require.Nil(t, fileArtifactMountDirPath.SetKey(starlark.String("file_1"), starlark.String("path/to/file/1")))
	require.Nil(t, fileArtifactMountDirPath.SetKey(starlark.String("file_2"), starlark.String("path/to/file/2")))
	serviceConfigDict["files"] = fileArtifactMountDirPath

	addServiceInstruction := newEmptyAddServiceInstruction(
		nil,
		nil,
		kurtosis_instruction.NewInstructionPosition(22, 26, "dummyFile"),
	)
	addServiceInstruction.starlarkKwargs[serviceIdArgName] = starlark.String("example-datastore-server-2")
	addServiceInstruction.starlarkKwargs[serviceConfigArgName] = starlarkstruct.FromStringDict(starlarkstruct.Default, serviceConfigDict)

	expectedOutput := `# from: dummyFile[22:26]
add_service(
	config=struct(
		cmd=[
			"bash",
			"-c",
			"/apps/main.py",
			1234
		],
		entrypoint=[
			"127.0.0.0",
			1234
		],
		env_vars={
			"VAR_1": "VALUE_1",
			"VAR_2": "VALUE_2"
		},
		files={
			"file_1": "path/to/file/1",
			"file_2": "path/to/file/2"
		},
		image="kurtosistech/example-datastore-server",
		ports={
			"grpc": struct(
				number=1234,
				protocol="TCP"
			)
		}
	),
	service_id="example-datastore-server-2"
)`
	require.Equal(t, expectedOutput, addServiceInstruction.GetCanonicalInstruction())
}

func TestAddServiceInstruction_EntryPointArgsAreReplaced(t *testing.T) {
	ipAddresses := map[service.ServiceID]net.IP{
		"foo_service": net.ParseIP("172.17.3.13"),
	}
	serviceNetwork := service_network.NewMockServiceNetwork(ipAddresses)
	addServiceInstruction := NewAddServiceInstruction(
		serviceNetwork,
		kurtosis_instruction.NewInstructionPosition(22, 26, "dummyFile"),
		"example-datastore-server-2",
		services.NewServiceConfigBuilder(
			testContainerImageName,
		).WithPrivatePorts(
			map[string]*kurtosis_core_rpc_api_bindings.Port{
				"grpc": {
					Number:   1323,
					Protocol: kurtosis_core_rpc_api_bindings.Port_TCP,
				},
			},
		).WithEntryPointArgs(
			[]string{"-- {{kurtosis:foo_service.ip_address}}"},
		).Build(),
		starlark.StringDict{}, // Unused
	)

	err := addServiceInstruction.replaceMagicStrings()
	require.Nil(t, err)
	require.Equal(t, "-- 172.17.3.13", addServiceInstruction.serviceConfig.EntrypointArgs[0])
}
