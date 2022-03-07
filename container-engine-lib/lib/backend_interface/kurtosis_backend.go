package backend_interface

import (
	"context"
	engine2 "github.com/kurtosis-tech/container-engine-lib/lib/backend_interface/objects/engine"
	"github.com/kurtosis-tech/container-engine-lib/lib/backend_interface/objects/module"
	"github.com/kurtosis-tech/container-engine-lib/lib/backend_interface/objects/port_spec"
	"net"
)

type KurtosisBackend interface {
	// Creates an engine with the given parameters
	CreateEngine(
		ctx context.Context,
		imageOrgAndRepo string,
		imageVersionTag string,
		grpcPortNum uint16,
		grpcProxyPortNum uint16,
		engineDataDirpathOnHostMachine string,
		envVars map[string]string,
	) (
		*engine2.Engine,
		error,
	)

	// Gets engines using the given filters, returning a map of matched engines identified by their engine ID
	GetEngines(ctx context.Context, filters *engine2.EngineFilters) (map[string]*engine2.Engine, error)

	// Stops the engines with the given IDs
	StopEngines(
		ctx context.Context,
		filters *engine2.EngineFilters,
	) (
		successfulEngineIds map[string]bool, // "set" of engine IDs that were successfully stopped
		erroredEngineIds map[string]error, // "set" of engine IDs that errored when stopping, with the error
		resultErr error, // Represents an error with the function itself, rather than the engines
	)

	// Destroys the engines with the given IDs, regardless of if they're running or not
	DestroyEngines(
		ctx context.Context,
		filters *engine2.EngineFilters,
	) (
		successfulEngineIds map[string]bool, // "set" of engine IDs that were successfully destroyed
		erroredEngineIds map[string]error, // "set" of engine IDs that errored when destroying, with the error
		resultErr error, // Represents an error with the function itself, rather than the engines
	)

	// Gets modules using the given filters, returning a map of matched modules identified by their module ID
	GetModules(ctx context.Context, filters *module.ModuleFilters) (map[string]*module.Module, error)

	// Creates a user service inside an enclave with the given configuration
	CreateUserService(
		ctx context.Context,
		id string,
		containerImageName string,
		privatePorts []*port_spec.PortSpec,
		entrypointArgs []string,
		cmdArgs []string,
		envVars map[string]string,
		enclaveDataDirMntDirpath string,
		filesArtifactMountDirpaths map[string]string,
    )(
		maybePublicIpAddr net.IP, // The ip exposed in the host machine. Will be nil if the service doesn't declare any private ports
		publicPorts map[string]*port_spec.PortSpec, //Mapping of port-used-by-service -> port-on-the-host-machine where the user can make requests to the port to access the port. If a used port doesn't have a host port bound, then the value will be nil.
		resultErr error,
	)

}
