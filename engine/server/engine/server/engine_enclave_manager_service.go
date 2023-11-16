package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"

	"github.com/kurtosis-tech/kurtosis/engine/server/engine/enclave_manager"
	"github.com/kurtosis-tech/kurtosis/engine/server/engine/types"
	"github.com/kurtosis-tech/kurtosis/engine/server/engine/utils"
	"github.com/kurtosis-tech/kurtosis/grpc-file-transfer/golang/grpc_file_streaming"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_http_api_bindings"
	api "github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_http_api_bindings"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_rpc_api_bindings"
)

func toHttpFilesArtifactNameAndUuid(rpc_artifact *kurtosis_core_rpc_api_bindings.FilesArtifactNameAndUuid) kurtosis_core_http_api_bindings.FilesArtifactNameAndUuid {
	return kurtosis_core_http_api_bindings.FilesArtifactNameAndUuid{
		FileName: &rpc_artifact.FileName,
		FileUuid: &rpc_artifact.FileUuid,
	}
}

func toHttpIdentifierArtifacts(rpc_artifact_list *kurtosis_core_rpc_api_bindings.ListFilesArtifactNamesAndUuidsResponse) []kurtosis_core_http_api_bindings.FilesArtifactNameAndUuid {
	return utils.MapList(rpc_artifact_list.FileNamesAndUuids, toHttpFilesArtifactNameAndUuid)
}

func toHttpUploadFilesArtifactResponse(rpc_upload_artifact *kurtosis_core_rpc_api_bindings.UploadFilesArtifactResponse) api.UploadFilesArtifactResponse {
	return api.UploadFilesArtifactResponse{
		Name: &rpc_upload_artifact.Name,
		Uuid: &rpc_upload_artifact.Uuid,
	}
}

type enclaveRuntime struct {
	enclaveManager           *enclave_manager.EnclaveManager
	remoteApiContainerClient map[string]kurtosis_core_rpc_api_bindings.ApiContainerServiceClient
}

func NewEnclaveRuntime(ctx context.Context, manager *enclave_manager.EnclaveManager) (*enclaveRuntime, error) {
	enclaves, err := manager.GetEnclaves(ctx)
	if err != nil {
		return nil, err
	}

	clients := map[string]kurtosis_core_rpc_api_bindings.ApiContainerServiceClient{}
	for uuid, info := range enclaves {
		conn, err := getGrpcClientConn(info)
		if err != nil {
			logrus.Errorf("Failed to establish gRPC connection with enclave manager container %s on %s", uuid, info.ApiContainerInfo)
			return nil, err
		}
		logrus.Debugf("Creating gRPC client to enclave manager container %s on %s", uuid, info.ApiContainerInfo)
		apiContainerClient := kurtosis_core_rpc_api_bindings.NewApiContainerServiceClient(conn)
		clients[uuid] = apiContainerClient
	}

	runtime := enclaveRuntime{
		enclaveManager:           manager,
		remoteApiContainerClient: clients,
	}

	return &runtime, nil
}

// ===============================================================================================================
// ============================= Implementing  StrictServerInterface =============================================
// ===============================================================================================================

// (GET /enclaves/{enclave_identifier}/artifacts)
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierArtifacts(ctx context.Context, request api.GetEnclavesEnclaveIdentifierArtifactsRequestObject) (api.GetEnclavesEnclaveIdentifierArtifactsResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)

	artifacts, err := apiContainerClient.ListFilesArtifactNamesAndUuids(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, err
	}

	http_artifacts := toHttpIdentifierArtifacts(artifacts)
	result := api.ListFilesArtifactNamesAndUuidsResponse{
		FileNamesAndUuids: &http_artifacts,
	}
	return api.GetEnclavesEnclaveIdentifierArtifacts200JSONResponse(result), nil

}

// (POST /enclaves/{enclave_identifier}/artifacts/local-file)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierArtifactsLocalFile(ctx context.Context, request api.PostEnclavesEnclaveIdentifierArtifactsLocalFileRequestObject) (api.PostEnclavesEnclaveIdentifierArtifactsLocalFileResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)
	logrus.Infof("Uploading file artifact to enclave %s", enclave_identifier)

	uploaded_artifacts := map[string]api.UploadFilesArtifactResponse{}
	for {
		// Get next part (file) from the the multipart POST request
		part, err := request.Body.NextPart()
		if err == io.EOF {
			break
		}
		filename := part.FileName()

		client, err := apiContainerClient.UploadFilesArtifact(ctx)
		if err != nil {
			return nil, stacktrace.Propagate(err, "Can't start file upload gRPC call with enclave %s", enclave_identifier)
		}
		clientStream := grpc_file_streaming.NewClientStream[kurtosis_core_rpc_api_bindings.StreamedDataChunk, kurtosis_core_rpc_api_bindings.UploadFilesArtifactResponse](client)

		response, err := clientStream.SendData(
			filename,
			part,
			0, // Length unknown head of time
			func(previousChunkHash string, contentChunk []byte) (*kurtosis_core_rpc_api_bindings.StreamedDataChunk, error) {
				return &kurtosis_core_rpc_api_bindings.StreamedDataChunk{
					Data:              contentChunk,
					PreviousChunkHash: previousChunkHash,
					Metadata: &kurtosis_core_rpc_api_bindings.DataChunkMetadata{
						Name: filename,
					},
				}, nil
			},
		)

		// The response is nil when a file artifact with the same has already been uploaded
		// TODO (edgar) Is this the expected behavior? If so, we should be explicit about it.
		if response != nil {
			artifact_response := toHttpUploadFilesArtifactResponse(response)
			uploaded_artifacts[filename] = artifact_response
		}
	}

	return api.PostEnclavesEnclaveIdentifierArtifactsLocalFile200JSONResponse(uploaded_artifacts), nil
}

// (PUT /enclaves/{enclave_identifier}/artifacts/remote-file)
func (manager *enclaveRuntime) PutEnclavesEnclaveIdentifierArtifactsRemoteFile(ctx context.Context, request api.PutEnclavesEnclaveIdentifierArtifactsRemoteFileRequestObject) (api.PutEnclavesEnclaveIdentifierArtifactsRemoteFileResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)
	logrus.Infof("Uploading file artifact to enclave %s", enclave_identifier)

	uploadStreamingCall, err := apiContainerClient.UploadFilesArtifact(ctx)
	if err != nil {
		logrus.Errorf("Can't start file upload gRPC call with enclave %s, error: %s", enclave_identifier, err)
		return nil, stacktrace.NewError("Can't start file upload gRPC call with enclave %s", enclave_identifier)
	}

	remoteFile, err := http.Get(*request.Body.Url)
	if err != nil {
		logrus.Errorf("Failed to retrieve remote file %s, error: %s", *request.Body.Url, err)
		return nil, err
	}

	buf := make([]byte, 1024)
	var n int
	hasher := sha1.New()
	previousChunkHash := ""

	for {
		n, err = remoteFile.Body.Read(buf)
		if err == io.EOF {
			break
		}
		chunk := kurtosis_core_rpc_api_bindings.StreamedDataChunk{
			Data:              buf[:n],
			PreviousChunkHash: previousChunkHash,
		}
		uploadStreamingCall.Send(&chunk)
		hasher.Reset()
		hasher.Write(chunk.Data)
		previousChunkHash = hex.EncodeToString(hasher.Sum(nil))
	}

	artifact_info, closing_err := uploadStreamingCall.CloseAndRecv()
	if closing_err != nil {
		logrus.Errorf("Failed to close upload gRPC call with enclave %s, error: %s", enclave_identifier, closing_err)
		return nil, closing_err
	}

	artifact_response := toHttpUploadFilesArtifactResponse(artifact_info)

	return api.PutEnclavesEnclaveIdentifierArtifactsRemoteFile200JSONResponse(artifact_response), nil
}

// (PUT /enclaves/{enclave_identifier}/artifacts/services/{service_identifier})
func (manager *enclaveRuntime) PutEnclavesEnclaveIdentifierArtifactsServicesServiceIdentifier(ctx context.Context, request api.PutEnclavesEnclaveIdentifierArtifactsServicesServiceIdentifierRequestObject) (api.PutEnclavesEnclaveIdentifierArtifactsServicesServiceIdentifierResponseObject, error) {
	return nil, Error{}
}

// (GET /enclaves/{enclave_identifier}/artifacts/{artifact_identifier})
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifier(ctx context.Context, request api.GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierRequestObject) (api.GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierResponseObject, error) {
	return nil, Error{}
}

// (GET /enclaves/{enclave_identifier}/artifacts/{artifact_identifier}/download)
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierDownload(ctx context.Context, request api.GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierDownloadRequestObject) (api.GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierDownloadResponseObject, error) {
	return nil, Error{}
}

// (GET /enclaves/{enclave_identifier}/services)
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierServices(ctx context.Context, request api.GetEnclavesEnclaveIdentifierServicesRequestObject) (api.GetEnclavesEnclaveIdentifierServicesResponseObject, error) {
	return nil, Error{}
}

// (POST /enclaves/{enclave_identifier}/services/connection)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierServicesConnection(ctx context.Context, request api.PostEnclavesEnclaveIdentifierServicesConnectionRequestObject) (api.PostEnclavesEnclaveIdentifierServicesConnectionResponseObject, error) {
	return nil, Error{}
}

// (GET /enclaves/{enclave_identifier}/services/{service_identifier})
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierServicesServiceIdentifier(ctx context.Context, request api.GetEnclavesEnclaveIdentifierServicesServiceIdentifierRequestObject) (api.GetEnclavesEnclaveIdentifierServicesServiceIdentifierResponseObject, error) {
	return nil, Error{}
}

// (POST /enclaves/{enclave_identifier}/services/{service_identifier}/command)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierServicesServiceIdentifierCommand(ctx context.Context, request api.PostEnclavesEnclaveIdentifierServicesServiceIdentifierCommandRequestObject) (api.PostEnclavesEnclaveIdentifierServicesServiceIdentifierCommandResponseObject, error) {
	return nil, Error{}
}

// (POST /enclaves/{enclave_identifier}/services/{service_identifier}/endpoints/{port_number}/availability)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierServicesServiceIdentifierEndpointsPortNumberAvailability(ctx context.Context, request api.PostEnclavesEnclaveIdentifierServicesServiceIdentifierEndpointsPortNumberAvailabilityRequestObject) (api.PostEnclavesEnclaveIdentifierServicesServiceIdentifierEndpointsPortNumberAvailabilityResponseObject, error) {
	return nil, Error{}
}

// (GET /enclaves/{enclave_identifier}/starlark)
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierStarlark(ctx context.Context, request api.GetEnclavesEnclaveIdentifierStarlarkRequestObject) (api.GetEnclavesEnclaveIdentifierStarlarkResponseObject, error) {
	return nil, Error{}
}

// (POST /enclaves/{enclave_identifier}/starlark/packages)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierStarlarkPackages(ctx context.Context, request api.PostEnclavesEnclaveIdentifierStarlarkPackagesRequestObject) (api.PostEnclavesEnclaveIdentifierStarlarkPackagesResponseObject, error) {
	return nil, Error{}
}

// (POST /enclaves/{enclave_identifier}/starlark/packages/{package_id})
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierStarlarkPackagesPackageId(ctx context.Context, request api.PostEnclavesEnclaveIdentifierStarlarkPackagesPackageIdRequestObject) (api.PostEnclavesEnclaveIdentifierStarlarkPackagesPackageIdResponseObject, error) {
	return nil, Error{}
}

// (POST /enclaves/{enclave_identifier}/starlark/scripts)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierStarlarkScripts(ctx context.Context, request api.PostEnclavesEnclaveIdentifierStarlarkScriptsRequestObject) (api.PostEnclavesEnclaveIdentifierStarlarkScriptsResponseObject, error) {
	return nil, Error{}
}

// ===============================================================================================================
// ===============================================================================================================
// ===============================================================================================================

// GetGrpcClientConn returns a client conn dialed in to the local port
// It is the caller's responsibility to call resultClientConn.close()
func getGrpcClientConn(enclaveInfo *types.EnclaveInfo) (resultClientConn *grpc.ClientConn, resultErr error) {
	// apiContainerGrpcPort := enclaveInfo.ApiContainerInfo.GrpcPortInsideEnclave
	// apiContainerIP := enclaveInfo.ApiContainerInfo.ContainerId
	apiContainerGrpcPort := enclaveInfo.ApiContainerHostMachineInfo.GrpcPortOnHostMachine
	apiContainerIP := enclaveInfo.ApiContainerHostMachineInfo.IpOnHostMachine
	grpcServerAddress := fmt.Sprintf("%v:%v", apiContainerIP, apiContainerGrpcPort)
	grpcConnection, err := grpc.Dial(grpcServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, stacktrace.Propagate(err, "Expected to be able to create a GRPC client connection on address '%v', but a non-nil error was returned", grpcServerAddress)
	}
	return grpcConnection, nil
}

func (manager enclaveRuntime) GetGrpcClientForEnclaveUUID(enclave_uuid string) kurtosis_core_rpc_api_bindings.ApiContainerServiceClient {
	client, found := manager.remoteApiContainerClient[enclave_uuid]
	if !found {
		// TODO(edgar): add logic to retry/refresh map
		panic(fmt.Sprintf("can't find enclave %s", enclave_uuid))
	}
	return client
}
