package execute

import (
	"context"
	"fmt"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/enclaves"
	"github.com/kurtosis-tech/kurtosis/api/golang/engine/kurtosis_engine_rpc_api_bindings"
	"github.com/kurtosis-tech/kurtosis/api/golang/engine/lib/kurtosis_context"
	"github.com/kurtosis-tech/kurtosis/cli/cli/command_framework/highlevel/engine_consuming_kurtosis_command"
	"github.com/kurtosis-tech/kurtosis/cli/cli/command_framework/lowlevel/args"
	"github.com/kurtosis-tech/kurtosis/cli/cli/command_framework/lowlevel/flags"
	"github.com/kurtosis-tech/kurtosis/cli/cli/command_str_consts"
	"github.com/kurtosis-tech/kurtosis/cli/cli/helpers/execution_ids"
	"github.com/kurtosis-tech/kurtosis/container-engine-lib/lib/backend_interface"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/sirupsen/logrus"
	"k8s.io/utils/strings"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

const (
	startosisScriptPathKey = "script-path"

	enclaveIdFlagKey                   = "enclave-id"
	defaultEnclaveId                   = ""
	disallowedCharInEnclaveIdRegexp    = "[^-A-Za-z0-9.]+"
	enclaveIdDisallowedCharReplacement = "-"

	isPartitioningEnabledFlagKey = "with-partitioning"
	defaultIsPartitioningEnabled = false

	engineClientCtxKey = "engine-client"
)

var StartosisExecCmd = &engine_consuming_kurtosis_command.EngineConsumingKurtosisCommand{
	CommandStr:             command_str_consts.StartosisExecCmdStr,
	ShortDescription:       "Execute a Startosis script in an enclave",
	LongDescription:        "Build an enclave from scratch using a Startosis script",
	EngineClientContextKey: engineClientCtxKey,
	Flags: []*flags.FlagConfig{
		{
			Key: enclaveIdFlagKey,
			Usage: fmt.Sprintf(
				"The ID to give the enclave that will be created to execute the module inside, which must match regex '%v' (default: use a string derived from the script filename and the current Unix time)",
				execution_ids.AllowedEnclaveIdCharsRegexStr,
			),
			Type:    flags.FlagType_String,
			Default: defaultEnclaveId,
		},
		{
			Key:     isPartitioningEnabledFlagKey,
			Usage:   "If set to true, the enclave that the module executes in will have partitioning enabled so network partitioning simulations can be run",
			Type:    flags.FlagType_Bool,
			Default: strconv.FormatBool(defaultIsPartitioningEnabled),
		},
	},
	Args: []*args.ArgConfig{
		&args.ArgConfig{
			Key:            startosisScriptPathKey,
			IsOptional:     false,
			DefaultValue:   "",
			IsGreedy:       false,
			ValidationFunc: validateScriptPath,
		},
	},
	RunFunc: run,
}

func run(
	ctx context.Context,
	_ backend_interface.KurtosisBackend,
	_ kurtosis_engine_rpc_api_bindings.EngineServiceClient,
	flags *flags.ParsedFlags,
	args *args.ParsedArgs,
) error {
	// Args parsing and validation
	userRequestedEnclaveId, err := flags.GetString(enclaveIdFlagKey)
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred getting the enclave ID using flag key '%s'", enclaveIdFlagKey)
	}
	isPartitioningEnabled, err := flags.GetBool(isPartitioningEnabledFlagKey)
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred getting the is-partitioning-enabled setting using flag key '%v'", isPartitioningEnabledFlagKey)
	}

	startosisScriptPath, err := args.GetNonGreedyArg(startosisScriptPathKey)
	if err != nil {
		return stacktrace.Propagate(err, "Error reading the Startosis script file at '%s'. Does it exist?", startosisScriptPath)
	}
	fileContentBytes, err := os.ReadFile(startosisScriptPath)
	if err != nil {
		return stacktrace.Propagate(err, "Unable to read content of script file '%s'", startosisScriptPath)
	}

	// Get or create enclave in Kurtosis
	enclaveIdStr := userRequestedEnclaveId
	if userRequestedEnclaveId == defaultEnclaveId {
		enclaveIdStr = getAutoGeneratedEnclaveIdStr(startosisScriptPath)
	}
	err = execution_ids.ValidateEnclaveId(enclaveIdStr)
	if err != nil {
		return stacktrace.Propagate(err, "Invalid enclave ID '%s'", enclaveIdStr)
	}
	enclaveId := enclaves.EnclaveID(enclaveIdStr)

	kurtosisCtx, err := kurtosis_context.NewKurtosisContextFromLocalEngine()
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred connecting to the local Kurtosis engine")
	}

	enclaveCtx, err := getOrCreateEnclaveContext(ctx, enclaveId, kurtosisCtx, isPartitioningEnabled)
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred getting the enclave context for enclave '%v'", enclaveId)
	}

	scriptOutput, interpretationError, validationError, executionError, err := enclaveCtx.ExecuteStartosisScript(string(fileContentBytes))
	if err != nil {
		return stacktrace.Propagate(err, "An unexpected error occurred executing the Startosis script '%s'", startosisScriptPath)
	}
	if interpretationError != "" {
		return stacktrace.NewError("There was an error interpreting the Startosis script '%s': \n%v", startosisScriptPath, interpretationError)
	}
	if validationError != "" {
		return stacktrace.NewError("There was an error validating the Startosis script '%s': \n%v", startosisScriptPath, validationError)
	}
	if executionError != "" {
		return stacktrace.NewError("There was an error executing the Startosis script '%s': \n%v", startosisScriptPath, executionError)
	}

	logrus.Infof("Startosis script executed successfully. Output of the script was: \n%v", scriptOutput)
	return nil
}

func validateScriptPath(ctx context.Context, flags *flags.ParsedFlags, args *args.ParsedArgs) error {
	scriptPath, err := args.GetNonGreedyArg(startosisScriptPathKey)
	if scriptPath == "" || err != nil {
		return stacktrace.Propagate(err, "Unable to get script-path argument. It should be non empty")
	}

	fileInfo, err := os.Stat(scriptPath)
	if err != nil {
		return stacktrace.Propagate(err, "Error reading script file")
	}
	if !fileInfo.Mode().IsRegular() {
		return stacktrace.Propagate(err, "Script path should points to a file on disk")
	}
	return nil
}

func getAutoGeneratedEnclaveIdStr(scriptFilePath string) string {
	pattern := regexp.MustCompile(disallowedCharInEnclaveIdRegexp)
	cleanedEnclaveIdStr := pattern.ReplaceAllString(filepath.Base(scriptFilePath), enclaveIdDisallowedCharReplacement)

	epochStrSuffix := fmt.Sprintf("--%v", time.Now().Unix())
	// Enclave ID have a max length. Truncate filename if it's too long here
	filenameMaxLength := execution_ids.EnclaveIdMaxLength - len(epochStrSuffix)
	cleanedEnclaveIdStr = strings.ShortenString(cleanedEnclaveIdStr, filenameMaxLength)
	return fmt.Sprintf("%v%v", cleanedEnclaveIdStr, epochStrSuffix)
}

func getOrCreateEnclaveContext(ctx context.Context, enclaveId enclaves.EnclaveID, kurtosisContext *kurtosis_context.KurtosisContext, isPartitioningEnabled bool) (*enclaves.EnclaveContext, error) {
	enclavesMap, err := kurtosisContext.GetEnclaves(ctx)
	if err != nil {
		return nil, stacktrace.Propagate(err, "Unable to get existing enclaves from Kurtosis backend")
	}
	if _, found := enclavesMap[enclaveId]; found {
		enclaveContext, err := kurtosisContext.GetEnclaveContext(ctx, enclaveId)
		if err != nil {
			return nil, stacktrace.Propagate(err, "Unable to get enclave context from the existing enclave '%s'", enclaveId)
		}
		return enclaveContext, nil
	}
	enclaveContext, err := kurtosisContext.CreateEnclave(ctx, enclaveId, isPartitioningEnabled)
	if err != nil {
		return nil, stacktrace.Propagate(err, fmt.Sprintf("Unable to create new enclave with ID '%s'", enclaveId))
	}
	return enclaveContext, nil
}
