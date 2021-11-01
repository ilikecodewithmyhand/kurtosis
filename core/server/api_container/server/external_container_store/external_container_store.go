/*
 * Copyright (c) 2021 - present Kurtosis Technologies Inc.
 * All Rights Reserved.
 */

package external_container_store

import (
	"github.com/google/uuid"
	"github.com/kurtosis-tech/kurtosis-core/server/commons"
	"github.com/palantir/stacktrace"
	"net"
	"sync"
)

type openRegistrationInfo struct {
	ipAddr net.IP
}

type registeredContainerInfo struct {
	ipAddr net.IP
}


// Sometimes, containers not started by the API container will need to run inside the enclave; this is how the API container tracks those containers
// NOTE: As of 2021-10-18, we actually don't use the stored information in any way
type ExternalContainerStore struct {
	freeIpAddrTracker *commons.FreeIpAddrTracker
	
	mutex *sync.Mutex

	// Map of registration_key -> info about an open registration
	openRegistrations map[string]openRegistrationInfo

	// Map of container_id -> info about a registered container
	registeredContainers map[string]registeredContainerInfo
}

func NewExternalContainerStore(freeIpAddrTracker *commons.FreeIpAddrTracker) *ExternalContainerStore {
	return &ExternalContainerStore{
		freeIpAddrTracker: freeIpAddrTracker,
		mutex: &sync.Mutex{},
		openRegistrations: map[string]openRegistrationInfo{},
		registeredContainers: map[string]registeredContainerInfo{},
	}
}

func (store *ExternalContainerStore) StartRegistration() (string, net.IP, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	registrationUuid, err := uuid.NewUUID()
	if err != nil {
		return "", nil, stacktrace.Propagate(err, "Couldn't generate a UUID to use for the registration key")
	}
	registrationKey := registrationUuid.String()

	ipAddr, err := store.freeIpAddrTracker.GetFreeIpAddr()
	if err != nil {
		return "", nil, stacktrace.Propagate(err, "An error occurred getting an IP address to give to the external container")
	}

	store.openRegistrations[registrationKey] = openRegistrationInfo{ipAddr: ipAddr}
	return registrationKey, ipAddr, nil
}

func (store *ExternalContainerStore) FinishRegistration(registrationKey, containerId string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	if containerId == "" {
		return stacktrace.NewError("Container ID to register cannot be empty")
	}

	registrationInfo, found := store.openRegistrations[registrationKey]
	if !found {
		return stacktrace.NewError("No registration is ongoing for key '%v'", registrationKey)
	}
	store.registeredContainers[containerId] = registeredContainerInfo{
		ipAddr: registrationInfo.ipAddr,
	}
	delete(store.openRegistrations, registrationKey)
	return nil
}

// ====================================================================================================
// 									   Private helper methods
// ====================================================================================================
