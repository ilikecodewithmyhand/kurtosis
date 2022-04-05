/*
 * Copyright (c) 2021 - present Kurtosis Technologies Inc.
 * All Rights Reserved.
 */

package service_network

import (
	"bytes"
	"context"
	"github.com/kurtosis-tech/container-engine-lib/lib/backend_impls/docker/docker_manager"
	"github.com/kurtosis-tech/container-engine-lib/lib/backend_interface/objects/service"
	"github.com/kurtosis-tech/free-ip-addr-tracker-lib/lib"
	"github.com/kurtosis-tech/kurtosis-core/launcher/enclave_container_launcher"
	"github.com/kurtosis-tech/kurtosis-core/server/api_container/server/service_network/networking_sidecar"
	"github.com/kurtosis-tech/kurtosis-core/server/api_container/server/service_network/partition_topology"
	"github.com/kurtosis-tech/kurtosis-core/server/api_container/server/service_network/service_network_types"
	"github.com/kurtosis-tech/kurtosis-core/server/api_container/server/service_network/user_service_launcher"
	"github.com/kurtosis-tech/kurtosis-core/server/commons/current_time_str_provider"
	"github.com/kurtosis-tech/kurtosis-core/server/commons/enclave_data_directory"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/sirupsen/logrus"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	defaultPartitionId                       service_network_types.PartitionID = "default"
	startingDefaultConnectionPacketLossValue                                   = 0
)

// Information that gets created with a service's registration
type serviceRegistrationInfo struct {
	privateIpAddr    net.IP
	serviceDirectory *enclave_data_directory.ServiceDirectory
}

// Information that gets created when a container is started for a service
type serviceRunInfo struct {
	// Service's container ID
	containerId string

	// Where the enclave data dir is bind-mounted on the service
	enclaveDataDirMntDirpath string

	// NOTE: When we want to make restart-able enclaves, we'll need to read these values from the container every time
	//  we need them (rather than storing them in-memory on the API container, which means the API container can't be restarted)
	privatePorts      map[string]*enclave_container_launcher.EnclaveContainerPort
	maybePublicIpAddr net.IP                                                      // Can be nil if the service doesn't declare any private ports
	publicPorts       map[string]*enclave_container_launcher.EnclaveContainerPort // Will be empty if the service doesn't declare any private ports
}

/*
This is the in-memory representation of the service network that the API container will manipulate. To make
	any changes to the test network, this struct must be used.
*/
type ServiceNetworkImpl struct {
	// When the network is destroyed, all requests will fail
	// This ensures that when the initializer tells the API container to destroy everything, the still-running
	//  testsuite can't create more work
	isDestroyed bool // VERY IMPORTANT TO CHECK AT THE START OF EVERY METHOD!

	mutex *sync.Mutex // VERY IMPORTANT TO CHECK AT THE START OF EVERY METHOD!

	// Whether partitioning has been enabled for this particular test
	isPartitioningEnabled bool

	freeIpAddrTracker *lib.FreeIpAddrTracker

	//TODO it must be replaced with kurtosisBackend after all functionality will be implemented
	dockerManager *docker_manager.DockerManager

	dockerNetworkId string

	enclaveDataDir *enclave_data_directory.EnclaveDataDirectory

	userServiceLauncher *user_service_launcher.UserServiceLauncher

	topology *partition_topology.PartitionTopology

	serviceIDsToGUIDs map[service_network_types.ServiceID]service.ServiceGUID

	// These are separate maps, rather than being bundled into a single containerInfo-valued map, because
	//  they're registered at different times (rather than in one atomic operation)
	serviceRegistrationInfo map[service.ServiceID]serviceRegistrationInfo
	serviceRunInfo          map[service_network_types.ServiceID]serviceRunInfo

	networkingSidecars map[service.ServiceGUID]networking_sidecar.NetworkingSidecarWrapper

	networkingSidecarManager networking_sidecar.NetworkingSidecarManager
}

func NewServiceNetworkImpl(
	isPartitioningEnabled bool,
	freeIpAddrTracker *lib.FreeIpAddrTracker,
	dockerManager *docker_manager.DockerManager,
	dockerNetworkId string,
	enclaveDataDir *enclave_data_directory.EnclaveDataDirectory,
	userServiceLauncher *user_service_launcher.UserServiceLauncher,
	networkingSidecarManager networking_sidecar.NetworkingSidecarManager) *ServiceNetworkImpl {
	defaultPartitionConnection := partition_topology.PartitionConnection{PacketLossPercentage: startingDefaultConnectionPacketLossValue}
	return &ServiceNetworkImpl{
		isDestroyed:           false,
		isPartitioningEnabled: isPartitioningEnabled,
		freeIpAddrTracker:     freeIpAddrTracker,
		dockerManager:         dockerManager,
		dockerNetworkId:       dockerNetworkId,
		enclaveDataDir:        enclaveDataDir,
		userServiceLauncher:   userServiceLauncher,
		mutex:                 &sync.Mutex{},
		topology: partition_topology.NewPartitionTopology(
			defaultPartitionId,
			defaultPartitionConnection,
		),
		serviceIDsToGUIDs:        map[service_network_types.ServiceID]service.ServiceGUID{},
		serviceRegistrationInfo:  map[service.ServiceGUID]serviceRegistrationInfo{},
		serviceRunInfo:           map[service_network_types.ServiceID]serviceRunInfo{},
		networkingSidecars:       map[service.ServiceGUID]networking_sidecar.NetworkingSidecarWrapper{},
		networkingSidecarManager: networkingSidecarManager,
	}
}

/*
Completely repartitions the network, throwing away the old topology
*/
func (network *ServiceNetworkImpl) Repartition(
	ctx context.Context,
	newPartitionServices map[service_network_types.PartitionID]map[service_network_types.ServiceID]bool,
	newPartitionConnections map[service_network_types.PartitionConnectionID]partition_topology.PartitionConnection,
	newDefaultConnection partition_topology.PartitionConnection) error {
	network.mutex.Lock()
	defer network.mutex.Unlock()
	if network.isDestroyed {
		return stacktrace.NewError("Cannot repartition; the service network has been destroyed")
	}

	if !network.isPartitioningEnabled {
		return stacktrace.NewError("Cannot repartition; partitioning is not enabled")
	}

	kurtosisBackendPartitionServices := map[service_network_types.PartitionID]map[service.ServiceID]bool{}
	for partitionId, serviceNetworkServiceIds := range newPartitionServices {
		kurtosisBackendServiceIds := map[service.ServiceID]bool{}
		for serviceNetworkServiceId := range serviceNetworkServiceIds {
			kurtosisBackendServiceIds[service.ServiceID(serviceNetworkServiceId)] = true
		}
		kurtosisBackendPartitionServices[partitionId] = kurtosisBackendServiceIds
	}

	if err := network.topology.Repartition(kurtosisBackendPartitionServices, newPartitionConnections, newDefaultConnection); err != nil {
		return stacktrace.Propagate(err, "An error occurred repartitioning the network topology")
	}

	servicePacketLossConfigurationsByServiceGUID, err := network.topology.GetServicePacketLossConfigurationsByServiceID()
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred getting the packet loss configuration by service GUID "+
			" after repartition, meaning that no partitions are actually being enforced!")
	}

	if err := updateTrafficControlConfiguration(ctx, servicePacketLossConfigurationsByServiceGUID, network.serviceRegistrationInfo, network.networkingSidecars); err != nil {
		return stacktrace.Propagate(err, "An error occurred updating the traffic control configuration to match the target service packet loss configurations after repartitioning")
	}
	return nil
}

// Registers a service for use with the network (creating the IPs and so forth), but doesn't start it
// If the partition ID is empty, registers the service with the default partition
func (network ServiceNetworkImpl) RegisterService(
	serviceId service_network_types.ServiceID,
	partitionId service_network_types.PartitionID) (net.IP, string, error) {
	// TODO extract this into a wrapper function that can be wrapped around every service call (so we don't forget)
	network.mutex.Lock()
	defer network.mutex.Unlock()
	if network.isDestroyed {
		return nil, "", stacktrace.NewError("Cannot register service with ID '%v'; the service network has been destroyed", serviceId)
	}

	if strings.TrimSpace(string(serviceId)) == "" {
		return nil, "", stacktrace.NewError("Service ID cannot be empty or whitespace")
	}

	serviceGuid := newServiceGUID(serviceId)

	if _, found := network.serviceRegistrationInfo[serviceGuid]; found {
		return nil, "", stacktrace.NewError("Cannot register service with ID '%v'; a service with that ID already exists", serviceId)
	}

	if partitionId == "" {
		partitionId = defaultPartitionId
	}
	if _, found := network.topology.GetPartitionServices()[partitionId]; !found {
		return nil, "", stacktrace.NewError(
			"No partition with ID '%v' exists in the current partition topology",
			partitionId,
		)
	}

	ip, err := network.freeIpAddrTracker.GetFreeIpAddr()
	if err != nil {
		return nil, "", stacktrace.Propagate(err, "An error occurred getting an IP for service with ID '%v'", serviceId)
	}
	shouldFreeIpAddr := true
	defer func() {
		// To keep our bookkeeping correct, if an error occurs later we need to back out the IP-adding that we do now
		if shouldFreeIpAddr {
			network.freeIpAddrTracker.ReleaseIpAddr(ip)
		}
	}()
	logrus.Debugf("Giving service '%v' IP '%v'", serviceId, ip.String())

	serviceDirectory, err := network.enclaveDataDir.GetServiceDirectory(serviceGuid)
	if err != nil {
		return nil, "", stacktrace.Propagate(err, "An error occurred creating a new service directory for service with GUID '%v'", serviceGuid)
	}

	serviceRegistrationInfo := serviceRegistrationInfo{
		privateIpAddr:    ip,
		serviceDirectory: serviceDirectory,
	}

	network.serviceRegistrationInfo[serviceGuid] = serviceRegistrationInfo
	network.serviceIDsToGUIDs[serviceId] = serviceGuid
	shouldUndoRegistrationInfoAdd := true
	defer func() {
		// If an error occurs, the service ID won't be used so we need to delete it from the map
		if shouldUndoRegistrationInfoAdd {
			delete(network.serviceRegistrationInfo, serviceGuid)
			delete(network.serviceIDsToGUIDs, serviceId)
		}
	}()

	if err := network.topology.AddService(serviceGuid, partitionId); err != nil {
		return nil, "", stacktrace.Propagate(
			err,
			"An error occurred adding service with GUID '%v' to partition '%v' in the topology",
			serviceGuid,
			partitionId)
	}

	shouldFreeIpAddr = false
	shouldUndoRegistrationInfoAdd = false
	return ip, serviceDirectory.GetDirpathRelativeToDataDirRoot(), nil
}

// TODO add tests for this
/*
Starts a previously-registered but not-started service by creating it in a container

Returns:
	Mapping of port-used-by-service -> port-on-the-Docker-host-machine where the user can make requests to the port
		to access the port. If a used port doesn't have a host port bound, then the value will be nil.
*/
func (network *ServiceNetworkImpl) StartService(
	ctx context.Context,
	serviceId service_network_types.ServiceID,
	imageName string,
	privatePorts map[string]*enclave_container_launcher.EnclaveContainerPort,
	entrypointArgs []string,
	cmdArgs []string,
	dockerEnvVars map[string]string,
	enclaveDataDirMntDirpath string,
	filesArtifactMountDirpaths map[string]string,
) (
	resultMaybePublicIpAddr net.IP, // Will be nil if the service doesn't declare any private ports
	resultPublicPorts map[string]*enclave_container_launcher.EnclaveContainerPort,
	resultErr error,
) {
	// TODO extract this into a wrapper function that can be wrapped around every service call (so we don't forget)
	network.mutex.Lock()
	defer network.mutex.Unlock()
	if network.isDestroyed {
		return nil, nil, stacktrace.NewError("Cannot start container for service with ID '%v'; the service network has been destroyed", serviceId)
	}

	serviceGuid := network.serviceIDsToGUIDs[serviceId]

	registrationInfo, registrationInfoFound := network.serviceRegistrationInfo[serviceGuid]
	if !registrationInfoFound {
		return nil, nil, stacktrace.NewError("Cannot start container for service with ID '%v'; no service with that GUID has been registered", serviceGuid)
	}
	if _, found := network.serviceRunInfo[serviceId]; found {
		return nil, nil, stacktrace.NewError("Cannot start container for service with ID '%v'; that service ID already has run information associated with it", serviceId)
	}
	serviceIpAddr := registrationInfo.privateIpAddr

	// When partitioning is enabled, there's a race condition where:
	//   a) we need to start the service before we can launch the sidecar but
	//   b) we can't modify the qdisc configuration until the sidecar container is launched.
	// This means that there's a period of time at startup where the container might not be partitioned. We solve
	//  this by setting the packet loss config of the new service in the already-existing services' qdisc.
	// This means that when the new service is launched, even if its own qdisc isn't yet updated, all the services
	//  it would communicate are already dropping traffic to it.
	if network.isPartitioningEnabled {
		servicePacketLossConfigurationsByServiceGUID, err := network.topology.GetServicePacketLossConfigurationsByServiceID()
		if err != nil {
			return nil, nil, stacktrace.Propagate(err, "An error occurred getting the packet loss configuration by service ID "+
				" to know what packet loss updates to apply on the new node")
		}

		servicesPacketLossConfigurationsWithoutNewNode := map[service.ServiceGUID]map[service.ServiceGUID]float32{}
		for serviceGuidInTopology, otherServicesPacketLossConfigs := range servicePacketLossConfigurationsByServiceGUID {
			if serviceGuid == serviceGuidInTopology {
				continue
			}
			servicesPacketLossConfigurationsWithoutNewNode[serviceGuidInTopology] = otherServicesPacketLossConfigs
		}

		if err := updateTrafficControlConfiguration(ctx, servicesPacketLossConfigurationsWithoutNewNode, network.serviceRegistrationInfo, network.networkingSidecars); err != nil {
			return nil, nil, stacktrace.Propagate(err, "An error occurred updating the traffic control configuration of all the other services "+
				"before adding the node, meaning that the node wouldn't actually start in a partition")
		}
	}

	serviceContainerId, maybeServicePublicIpAddr, servicePublicPorts, err := network.userServiceLauncher.Launch(
		ctx,
		serviceGuid,
		string(serviceId),
		serviceIpAddr,
		imageName,
		network.dockerNetworkId,
		privatePorts,
		entrypointArgs,
		cmdArgs,
		dockerEnvVars,
		enclaveDataDirMntDirpath,
		filesArtifactMountDirpaths)
	if err != nil {
		return nil, nil, stacktrace.Propagate(
			err,
			"An error occurred creating the user service")
	}
	runInfo := serviceRunInfo{
		containerId:              serviceContainerId,
		enclaveDataDirMntDirpath: enclaveDataDirMntDirpath,
		privatePorts:             privatePorts,
		maybePublicIpAddr:        maybeServicePublicIpAddr,
		publicPorts:              servicePublicPorts,
	}
	network.serviceRunInfo[serviceId] = runInfo

	if network.isPartitioningEnabled {
		sidecar, err := network.networkingSidecarManager.Add(ctx, serviceGuid)
		if err != nil {
			return nil, nil, stacktrace.Propagate(err, "An error occurred adding the networking sidecar")
		}
		network.networkingSidecars[serviceGuid] = sidecar

		if err := sidecar.InitializeTrafficControl(ctx); err != nil {
			return nil, nil, stacktrace.Propagate(err, "An error occurred initializing the newly-created networking-sidecar-traffic-control-qdisc-configuration")
		}

		// TODO Getting packet loss configuration by service GUID is an expensive call and, as of 2021-11-23, we do it twice - the solution is to make
		//  Getting packet loss configuration by service GUID not an expensive call
		servicePacketLossConfigurationsByServiceGUID, err := network.topology.GetServicePacketLossConfigurationsByServiceID()
		if err != nil {
			return nil, nil, stacktrace.Propagate(err, "An error occurred getting the packet loss configuration by service GUID "+
				" to know what packet loss updates to apply on the new node")
		}
		newNodeServicePacketLossConfiguration := servicePacketLossConfigurationsByServiceGUID[serviceGuid]
		updatesToApply := map[service.ServiceGUID]map[service.ServiceGUID]float32{
			serviceGuid: newNodeServicePacketLossConfiguration,
		}
		if err := updateTrafficControlConfiguration(ctx, updatesToApply, network.serviceRegistrationInfo, network.networkingSidecars); err != nil {
			return nil, nil, stacktrace.Propagate(err, "An error occurred applying the traffic control configuration on the new node to partition it "+
				"off from other nodes")
		}
	}

	return maybeServicePublicIpAddr, servicePublicPorts, nil
}

func (network *ServiceNetworkImpl) RemoveService(
	ctx context.Context,
	serviceId service_network_types.ServiceID,
	containerStopTimeout time.Duration) error {
	// TODO switch to a wrapper function
	network.mutex.Lock()
	defer network.mutex.Unlock()
	if network.isDestroyed {
		return stacktrace.NewError("Cannot remove service; the service network has been destroyed")
	}

	if err := network.removeServiceWithoutMutex(ctx, serviceId, containerStopTimeout); err != nil {
		return stacktrace.Propagate(err, "An error occurred removing service with ID '%v'", serviceId)
	}
	return nil
}

func (network *ServiceNetworkImpl) ExecCommand(
	ctx context.Context,
	serviceId service_network_types.ServiceID,
	command []string) (int32, string, error) {
	// NOTE: This will block all other operations while this command is running!!!! We might need to change this so it's
	// asynchronous
	network.mutex.Lock()
	defer network.mutex.Unlock()
	if network.isDestroyed {
		return 0, "", stacktrace.NewError("Cannot run exec command; the service network has been destroyed")
	}

	runInfo, found := network.serviceRunInfo[serviceId]
	if !found {
		return 0, "", stacktrace.NewError(
			"Could not run exec command '%v' against service '%v'; no container has been created for the service yet",
			command,
			serviceId)
	}

	// NOTE: This is a SYNCHRONOUS command, meaning that the entire network will be blocked until the command finishes
	// In the future, this will likely be insufficient

	execOutputBuf := &bytes.Buffer{}
	exitCode, err := network.dockerManager.RunExecCommand(ctx, runInfo.containerId, command, execOutputBuf)
	if err != nil {
		return 0, "", stacktrace.Propagate(
			err,
			"An error occurred running exec command '%v' against service '%v'",
			command,
			serviceId)
	}

	return exitCode, execOutputBuf.String(), nil
}

func (network *ServiceNetworkImpl) GetServiceRegistrationInfo(serviceId service_network_types.ServiceID) (
	privateIpAddr net.IP,
	relativeServiceDirpath string,
	resultErr error,
) {
	network.mutex.Lock()
	defer network.mutex.Unlock()
	if network.isDestroyed {
		return nil, "", stacktrace.NewError("Cannot get registration info for service '%v'; the service network has been destroyed", serviceId)
	}

	serviceGuid := network.serviceIDsToGUIDs[serviceId]

	registrationInfo, found := network.serviceRegistrationInfo[serviceGuid]
	if !found {
		return nil, "", stacktrace.NewError("No registration information found for service with GUID '%v'", serviceGuid)
	}

	return registrationInfo.privateIpAddr, registrationInfo.serviceDirectory.GetDirpathRelativeToDataDirRoot(), nil
}

func (network *ServiceNetworkImpl) GetServiceRunInfo(serviceId service_network_types.ServiceID) (
	privatePorts map[string]*enclave_container_launcher.EnclaveContainerPort,
	publicIpAddr net.IP,
	publicPorts map[string]*enclave_container_launcher.EnclaveContainerPort,
	enclaveDataDirMntDirpath string,
	resultErr error,
) {
	network.mutex.Lock()
	defer network.mutex.Unlock()
	if network.isDestroyed {
		return nil, nil, nil, "", stacktrace.NewError("Cannot get run info for service '%v'; the service network has been destroyed", serviceId)
	}

	runInfo, found := network.serviceRunInfo[serviceId]
	if !found {
		return nil, nil, nil, "", stacktrace.NewError("No run information found for service with ID '%v'", serviceId)
	}
	return runInfo.privatePorts, runInfo.maybePublicIpAddr, runInfo.publicPorts, runInfo.enclaveDataDirMntDirpath, nil
}

func (network *ServiceNetworkImpl) GetServiceIDs() map[service_network_types.ServiceID]bool {

	serviceIDs := make(map[service_network_types.ServiceID]bool, len(network.serviceRunInfo))

	for serviceId := range network.serviceRunInfo {
		if _, ok := serviceIDs[serviceId]; !ok {
			serviceIDs[serviceId] = true
		}
	}
	return serviceIDs
}

// ====================================================================================================
// 									   Private helper methods
// ====================================================================================================
func (network *ServiceNetworkImpl) removeServiceWithoutMutex(
	ctx context.Context,
	serviceId service_network_types.ServiceID,
	containerStopTimeout time.Duration) error {

	serviceGuid := network.serviceIDsToGUIDs[serviceId]

	registrationInfo, foundRegistrationInfo := network.serviceRegistrationInfo[serviceGuid]
	if !foundRegistrationInfo {
		return stacktrace.NewError("No registration info found for service '%v'", serviceId)
	}
	network.topology.RemoveService(serviceGuid)
	delete(network.serviceRegistrationInfo, serviceGuid)
	delete(network.serviceIDsToGUIDs, serviceId)

	// TODO PERF: Parallelize the shutdown of the service container and the sidecar container
	runInfo, foundRunInfo := network.serviceRunInfo[serviceId]
	if foundRunInfo {
		serviceContainerId := runInfo.containerId
		// Make a best-effort attempt to stop the service container
		logrus.Debugf("Stopping container ID '%v' for service ID '%v'...", serviceContainerId, serviceId)
		if err := network.dockerManager.StopContainer(ctx, serviceContainerId, containerStopTimeout); err != nil {
			return stacktrace.Propagate(err, "An error occurred stopping the container with ID %v", serviceContainerId)
		}
		delete(network.serviceRunInfo, serviceId)
		logrus.Debugf("Successfully stopped container ID '%v'", serviceContainerId)
		logrus.Debugf("Disconnecting container ID '%v' from network ID '%v'...", serviceContainerId, network.dockerNetworkId)
		//Disconnect the container from the network in order to free the network container's alias if a new service with same alias
		//is loaded in the network
		if err := network.dockerManager.DisconnectContainerFromNetwork(ctx, serviceContainerId, network.dockerNetworkId); err != nil {
			return stacktrace.Propagate(err, "An error occurred disconnecting the container with ID %v from network with ID %v", serviceContainerId, network.dockerNetworkId)
		}
		logrus.Debugf("Successfully disconnected container ID '%v'", serviceContainerId)
	}
	network.freeIpAddrTracker.ReleaseIpAddr(registrationInfo.privateIpAddr)

	sidecar, foundSidecar := network.networkingSidecars[serviceGuid]
	if network.isPartitioningEnabled && foundSidecar {
		// NOTE: As of 2020-12-31, we don't need to update the iptables of the other services in the network to
		//  clear the now-removed service's IP because:
		// 	 a) nothing is using it so it doesn't do anything and
		//	 b) all service's iptables get overwritten on the next Add/Repartition call
		// If we ever do incremental iptables though, we'll need to fix all the other service's iptables here!
		if err := network.networkingSidecarManager.Remove(ctx, sidecar); err != nil {
			return stacktrace.Propagate(err, "An error occurred destroying the sidecar for service with ID '%v'", serviceId)
		}
		delete(network.networkingSidecars, serviceGuid)
		logrus.Debugf("Successfully removed sidecar attached to service with GUID '%v'", serviceGuid)
	}

	return nil
}

/*
Updates the traffic control configuration of the services with the given IDs to match the target services packet loss configuration

NOTE: This is not thread-safe, so it must be within a function that locks mutex!
*/
func updateTrafficControlConfiguration(
	ctx context.Context,
	targetServicePacketLossConfigs map[service.ServiceGUID]map[service.ServiceGUID]float32,
	serviceRegistrationInfo map[service.ServiceGUID]serviceRegistrationInfo,
	networkingSidecars map[service.ServiceGUID]networking_sidecar.NetworkingSidecarWrapper) error {

	// TODO PERF: Run the container updates in parallel, with the container being modified being the most important

	for serviceGuid, allOtherServicesPacketLossConfigurations := range targetServicePacketLossConfigs {
		allPacketLossPercentageForIpAddresses := map[string]float32{}
		for otherServiceGuid, otherServicePacketLossPercentage := range allOtherServicesPacketLossConfigurations {

			infoForService, found := serviceRegistrationInfo[otherServiceGuid]
			if !found {
				return stacktrace.NewError(
					"Service with GUID '%v' needs to add packet loss configuration for service with GUID '%v', but the latter "+
						"doesn't have service registration info (i.e. an IP) associated with it",
					serviceGuid,
					otherServiceGuid)
			}

			allPacketLossPercentageForIpAddresses[infoForService.privateIpAddr.String()] = otherServicePacketLossPercentage
		}

		sidecar, found := networkingSidecars[serviceGuid]
		if !found {
			return stacktrace.NewError(
				"Need to update qdisc configuration of service with GUID '%v', but the service doesn't have a sidecar",
				serviceGuid)
		}

		if err := sidecar.UpdateTrafficControl(ctx, allPacketLossPercentageForIpAddresses); err != nil {
			return stacktrace.Propagate(
				err,
				"An error occurred updating the qdisc configuration for service '%v'",
				serviceGuid)
		}
	}
	return nil
}

func newServiceGUID(serviceID service_network_types.ServiceID) service.ServiceGUID {
	suffix := current_time_str_provider.GetCurrentTimeStr()
	return service.ServiceGUID(string(serviceID) + "-" + suffix)
}
