package engine_manager

import (
	"context"
	"fmt"
	"github.com/kurtosis-tech/container-engine-lib/lib/backend_interface"
	"github.com/kurtosis-tech/container-engine-lib/lib/backend_interface/objects/container_status"
	"github.com/kurtosis-tech/container-engine-lib/lib/backend_interface/objects/engine"
	"github.com/kurtosis-tech/kurtosis-engine-api-lib/api/golang/kurtosis_engine_rpc_api_bindings"
	"github.com/kurtosis-tech/object-attributes-schema-lib/schema"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"strconv"
	"strings"
	"time"
)

const (
	waitForEngineResponseTimeout = 5 * time.Second

	// --------------------------- Old port parsing constants ------------------------------------
	// These are the old labels that the API container used to use before 2021-11-15 for declaring its port num protocol
	// We can get rid of this after 2022-05-15, when we're confident no users will be running API containers with the old label
	pre2021_11_15_portNum   = uint16(9710)
	pre2021_11_15_portProto = schema.PortProtocol_TCP

	// These are the old labels that the API container used to use before 2021-12-02 for declaring its port num protocol
	// We can get rid of this after 2022-06-02, when we're confident no users will be running API containers with the old label
	pre2021_12_02_portNumLabel    = "com.kurtosistech.port-number"
	pre2021_12_02_portNumBase     = 10
	pre2021_12_02_portNumUintBits = 16
	pre2021_12_02_portProtocol    = schema.PortProtocol_TCP
	// --------------------------- Old port parsing constants ------------------------------------
)

// Unfortunately, Docker doesn't have constants for the protocols it supports declared
var objAttrsSchemaPortProtosToDockerPortProtos = map[schema.PortProtocol]string{
	schema.PortProtocol_TCP:  "tcp",
	schema.PortProtocol_SCTP: "sctp",
	schema.PortProtcol_UDP:   "udp",
}

type EngineManager struct {
	kurtosisBackend backend_interface.KurtosisBackend
	// Make engine IP, port, and protocol configurable in the future
}

func NewEngineManager(kurtosisBackend backend_interface.KurtosisBackend) *EngineManager {
	return &EngineManager{kurtosisBackend: kurtosisBackend}
}

/*
Returns:
	- The engine status
	- The host machine port bindings (not present if the engine is stopped)
	- The engine version (only present if the engine is running)
*/
func (manager *EngineManager) GetEngineStatus(
	ctx context.Context,
) (EngineStatus, *hostMachineIpAndPort, string, error) {
	runningEngineContainers, err := manager.kurtosisBackend.GetEngines(ctx, getRunningEnginesFilter())
	if err != nil {
		return "", nil, "", stacktrace.Propagate(err, "An error occurred getting Kurtosis engine containers")
	}

	numRunningEngineContainers := len(runningEngineContainers)
	if numRunningEngineContainers > 1 {
		return "", nil, "", stacktrace.NewError("Cannot report engine status because we found %v running Kurtosis engine containers; this is very strange as there should never be more than one", numRunningEngineContainers)
	} else if numRunningEngineContainers == 0 {
		return EngineStatus_Stopped, nil, "", nil
	}
	engineContainer := getFirstEngineFromMap(runningEngineContainers)

	runningEngineIpAndPort := &hostMachineIpAndPort{
		ipAddr:  engineContainer.GetPublicIPAddress(),
		portNum: engineContainer.GetPublicGRPCPort().GetNumber(),
	}

	engineClient, clientCloseFunc, err := getEngineClientFromHostMachineIpAndPort(runningEngineIpAndPort)
	if err != nil {
		return EngineStatus_ContainerRunningButServerNotResponding, runningEngineIpAndPort, "", nil
	}
	defer clientCloseFunc()

	engineInfo, err := getEngineInfoWithTimeout(ctx, engineClient)
	if err != nil {
		return EngineStatus_ContainerRunningButServerNotResponding, runningEngineIpAndPort, "", nil
	}

	return EngineStatus_Running, runningEngineIpAndPort, engineInfo.GetEngineVersion(), nil
}

// Starts an engine if one doesn't exist already, and returns a client to it
func (manager *EngineManager) StartEngineIdempotentlyWithDefaultVersion(ctx context.Context, logLevel logrus.Level) (kurtosis_engine_rpc_api_bindings.EngineServiceClient, func() error, error) {
	status, maybeHostMachinePortBinding, engineVersion, err := manager.GetEngineStatus(ctx)
	if err != nil {
		return nil, nil, stacktrace.Propagate(err, "An error occurred retrieving the Kurtosis engine status, which is necessary for creating a connection to the engine")
	}
	engineGuarantor := newEngineExistenceGuarantorWithDefaultVersion(
		ctx,
		maybeHostMachinePortBinding,
		manager.kurtosisBackend,
		logLevel,
		engineVersion,
	)
	engineClient, engineClientCloseFunc, err := startEngineWithGuarantor(ctx, status, engineGuarantor)
	if err != nil {
		return nil, nil, stacktrace.Propagate(err, "An error occurred starting the engine with the engine existence guarantor")
	}
	return engineClient, engineClientCloseFunc, nil
}

// Starts an engine if one doesn't exist already, and returns a client to it
func (manager *EngineManager) StartEngineIdempotentlyWithCustomVersion(ctx context.Context, engineImageVersionTag string, logLevel logrus.Level) (kurtosis_engine_rpc_api_bindings.EngineServiceClient, func() error, error) {
	status, maybeHostMachinePortBinding, engineVersion, err := manager.GetEngineStatus(ctx)
	if err != nil {
		return nil, nil, stacktrace.Propagate(err, "An error occurred retrieving the Kurtosis engine status, which is necessary for creating a connection to the engine")
	}
	engineGuarantor := newEngineExistenceGuarantorWithCustomVersion(
		ctx,
		maybeHostMachinePortBinding,
		manager.kurtosisBackend,
		engineImageVersionTag,
		logLevel,
		engineVersion,
	)
	engineClient, engineClientCloseFunc, err := startEngineWithGuarantor(ctx, status, engineGuarantor)
	if err != nil {
		return nil, nil, stacktrace.Propagate(err, "An error occurred starting the engine with the engine existence guarantor")
	}
	return engineClient, engineClientCloseFunc, nil
}

// Stops the engine if it's running, doing nothing if not
func (manager *EngineManager) StopEngineIdempotently(ctx context.Context) error {
	_, erroredEngineIds, err := manager.kurtosisBackend.StopEngines(ctx, getRunningEnginesFilter())
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred stopping ")
	}
	engineStopErrorStrs := []string{}
	for engineId, err := range erroredEngineIds {
		if err != nil {
			wrappedErr := stacktrace.Propagate(
				err,
				"An error occurred stopping engine container `%v'",
				engineId,
			)
			engineStopErrorStrs = append(engineStopErrorStrs, wrappedErr.Error())
		}
	}

	if len(engineStopErrorStrs) > 0 {
		return stacktrace.NewError(
			"One or more errors occurred stopping the engine(s):\n%v",
			strings.Join(
				engineStopErrorStrs,
				"\n\n",
			),
		)
	}

	return nil
}

// ====================================================================================================
//                                       Private Helper Functions
// ====================================================================================================
func startEngineWithGuarantor(ctx context.Context, currentStatus EngineStatus, engineGuarantor *engineExistenceGuarantor) (kurtosis_engine_rpc_api_bindings.EngineServiceClient, func() error, error) {
	if err := currentStatus.Accept(engineGuarantor); err != nil {
		return nil, nil, stacktrace.Propagate(err, "An error occurred guaranteeing that a Kurtosis engine is running")
	}
	hostMachinePortBinding := engineGuarantor.getPostVisitingHostMachineIpAndPort()

	engineClient, clientCloseFunc, err := getEngineClientFromHostMachineIpAndPort(hostMachinePortBinding)
	if err != nil {
		return nil, nil, stacktrace.Propagate(err, "An error occurred connecting to the running engine; this is very strange and likely indicates a bug in the engine itself")
	}

	// Final verification to ensure that the engine server is responding
	if _, err := getEngineInfoWithTimeout(ctx, engineClient); err != nil {
		return nil, nil, stacktrace.Propagate(err, "An error occurred connecting to the engine server; this is very strange and likely indicates a bug in the engine itself")
	}
	return engineClient, clientCloseFunc, nil
}

func getEngineClientFromHostMachineIpAndPort(hostMachineIpAndPort *hostMachineIpAndPort) (kurtosis_engine_rpc_api_bindings.EngineServiceClient, func() error, error) {
	url := fmt.Sprintf(
		"%v:%v",
		hostMachineIpAndPort.ipAddr.String(),
		hostMachineIpAndPort.portNum,
	)
	conn, err := grpc.Dial(url, grpc.WithInsecure())
	if err != nil {
		return nil, nil, stacktrace.Propagate(err, "An error occurred dialling Kurtosis engine at URL '%v'", url)
	}
	engineClient := kurtosis_engine_rpc_api_bindings.NewEngineServiceClient(conn)
	return engineClient, conn.Close, nil
}

func getEngineInfoWithTimeout(ctx context.Context, client kurtosis_engine_rpc_api_bindings.EngineServiceClient) (*kurtosis_engine_rpc_api_bindings.GetEngineInfoResponse, error) {
	ctxWithTimeout, cancelFunc := context.WithTimeout(ctx, waitForEngineResponseTimeout)
	defer cancelFunc()
	engineInfo, err := client.GetEngineInfo(ctxWithTimeout, &emptypb.Empty{}, grpc.WaitForReady(true))
	if err != nil {
		return nil, stacktrace.Propagate(
			err,
			"Kurtosis engine server didn't return a response even with %v timeout",
			waitForEngineResponseTimeout,
		)
	}
	return engineInfo, nil
}

func getPrivateEnginePort(containerLabels map[string]string) (*schema.PortSpec, error) {
	serializedPortSpecs, found := containerLabels[schema.PortSpecsLabel]
	if found {
		portSpecs, err := schema.DeserializePortSpecs(serializedPortSpecs)
		if err != nil {
			return nil, stacktrace.Propagate(err, "An error occurred deserializing engine server port spec string '%v'", serializedPortSpecs)
		}
		portSpec, foundInternalPortId := portSpecs[schema.KurtosisInternalContainerGRPCPortID]
		if !foundInternalPortId {
			return nil, stacktrace.NewError("No Kurtosis-internal port ID '%v' found in the engine server port specs", schema.KurtosisInternalContainerGRPCPortID)
		}
		return portSpec, nil
	}

	// We can get rid of this after 2022-06-02, when we're confident no users will be running API containers with this label
	pre2021_12_02Port, err := getApiContainerPrivatePortUsingPre2021_12_02Label(containerLabels)
	if err == nil {
		return pre2021_12_02Port, nil
	} else {
		logrus.Debugf("An error occurred getting the engine container private port num using the pre-2021-12-02 label: %v", err)
	}

	// We can get rid of this after 2022-05-15, when we're confident no users will be running API containers with this label
	pre2021_11_15Port, err := schema.NewPortSpec(pre2021_11_15_portNum, pre2021_11_15_portProto)
	if err != nil {
		return nil, stacktrace.Propagate(err, "Couldn't create engine private port spec using pre-2021-11-15 constants")
	}
	return pre2021_11_15Port, nil
}

func getApiContainerPrivatePortUsingPre2021_12_02Label(containerLabels map[string]string) (*schema.PortSpec, error) {
	// We can get rid of this after 2022-06-02, when we're confident no users will be running API containers with this label
	portNumStr, found := containerLabels[pre2021_12_02_portNumLabel]
	if !found {
		return nil, stacktrace.NewError("Couldn't get engine container private port using the pre-2021-12-02 label '%v' because it doesn't exist", pre2021_12_02_portNumLabel)
	}
	portNumUint64, err := strconv.ParseUint(portNumStr, pre2021_12_02_portNumBase, pre2021_12_02_portNumUintBits)
	if err != nil {
		return nil, stacktrace.Propagate(err, "An error occurred parsing pre-2021-12-02 private port num string '%v' to a uint16", portNumStr)
	}
	portNumUint16 := uint16(portNumUint64) // Safe to do because we pass in the number of bits to the ParseUint call above
	result, err := schema.NewPortSpec(portNumUint16, pre2021_12_02_portProtocol)
	if err != nil {
		return nil, stacktrace.Propagate(
			err,
			"An error occurred creating a new port spec using pre-2021-12-02 port num '%v' and protocol '%v'",
			portNumUint16,
			pre2021_12_02_portProtocol,
		)
	}
	return result, nil
}

// getRunningEnginesFilter returns a filter for engines with status engine.EngineStatus_Running
func getRunningEnginesFilter() *engine.EngineFilters {
	return &engine.EngineFilters{
		Statuses: map[container_status.ContainerStatus]bool{
			container_status.ContainerStatus_Running: true,
		},
	}
}

// getFirstEngineFromMap returns the first value iterated by the `range` statement on a map
// returns nil if the map is empty
func getFirstEngineFromMap(engineMap map[string]*engine.Engine) *engine.Engine {
	firstEngineInMap := (*engine.Engine)(nil)
	for _, engineInMap := range engineMap {
		firstEngineInMap = engineInMap
		break
	}
	return firstEngineInMap
}
