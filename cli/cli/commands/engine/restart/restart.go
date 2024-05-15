package restart

import (
	"context"
	"fmt"
	"github.com/kurtosis-tech/kurtosis/cli/cli/command_framework/lowlevel"
	"github.com/kurtosis-tech/kurtosis/cli/cli/command_framework/lowlevel/args"
	"github.com/kurtosis-tech/kurtosis/cli/cli/command_framework/lowlevel/flags"
	"github.com/kurtosis-tech/kurtosis/cli/cli/command_str_consts"
	"github.com/kurtosis-tech/kurtosis/cli/cli/commands/engine/common"
	"github.com/kurtosis-tech/kurtosis/cli/cli/defaults"
	"github.com/kurtosis-tech/kurtosis/cli/cli/helpers/engine_manager"
	"github.com/kurtosis-tech/kurtosis/cli/cli/helpers/logrus_log_levels"
	"github.com/kurtosis-tech/kurtosis/kurtosis_version"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/sirupsen/logrus"
	"strconv"
	"strings"
)

const (
	engineVersionFlagKey           = "version"
	engineAuthorFlagKey            = "author"
	logLevelFlagKey                = "log-level"
	enclavePoolSizeFlagKey         = "enclave-pool-size"
	githubAuthTokenOverrideFlagKey = "github-auth-token"

	defaultEngineVersion                   = ""
	defaultEngineAuthor                    = "kurtosistech"
	restartEngineOnSameVersionIfAnyRunning = false

	defaultShouldRestartAPIContainers = "false"
	restartAPIContainersFlagKey       = "restart-api-containers"
)

var RestartCmd = &lowlevel.LowlevelKurtosisCommand{
	CommandStr:       command_str_consts.EngineRestartCmdStr,
	ShortDescription: "Restart the Kurtosis engine",
	LongDescription:  "Stops any existing Kurtosis engine, then starts a new one",
	Args:             nil,
	Flags: []*flags.FlagConfig{
		{
			Key:       engineVersionFlagKey,
			Usage:     "The version (Docker tag) of the Kurtosis engine that should be started (blank will start the default version)",
			Shorthand: "",
			Type:      flags.FlagType_String,
			Default:   defaultEngineVersion,
		},
		{
			Key:       engineAuthorFlagKey,
			Usage:     "The author (Docker username) of the Kurtosis engine that should be started (blank will start the kurtosistech version)",
			Shorthand: "",
			Type:      flags.FlagType_String,
			Default:   defaultEngineAuthor,
		},
		{
			Key: logLevelFlagKey,
			Usage: fmt.Sprintf(
				"The level that the started engine should log at (%v)",
				strings.Join(
					logrus_log_levels.GetAcceptableLogLevelStrs(),
					"|",
				),
			),
			Shorthand: "",
			Type:      flags.FlagType_String,
			Default:   defaults.DefaultEngineLogLevel.String(),
		},
		{
			Key: enclavePoolSizeFlagKey,
			Usage: fmt.Sprintf(
				"The enclave pool size, the default value is '%v' which means it will be disabled. CAUTION: This is only available for Kubernetes, and this command will fail if you want to use it for Docker.",
				defaults.DefaultEngineEnclavePoolSize,
			),
			Shorthand: "",
			Type:      flags.FlagType_Uint8,
			Default:   strconv.Itoa(int(defaults.DefaultEngineEnclavePoolSize)),
		},
		{
			Key:       githubAuthTokenOverrideFlagKey,
			Usage:     "The GitHub auth token that should be used to authorize git operations such as accessing packages in private repositories. Overrides existing GitHub auth config if a user is logged in.",
			Shorthand: "",
			Type:      flags.FlagType_String,
			Default:   defaults.DefaultGitHubAuthTokenOverride,
		},
		{
			Key:       restartAPIContainersFlagKey,
			Usage:     "Also restart the current API containers after starting the Kurtosis engine.",
			Shorthand: "",
			Type:      flags.FlagType_Bool,
			Default:   defaultShouldRestartAPIContainers,
		},
	},
	PreValidationAndRunFunc:  nil,
	RunFunc:                  run,
	PostValidationAndRunFunc: nil,
}

func run(_ context.Context, flags *flags.ParsedFlags, _ *args.ParsedArgs) error {
	ctx := context.Background()

	enclavePoolSize, err := flags.GetUint8(enclavePoolSizeFlagKey)
	if err != nil {
		return stacktrace.Propagate(err, "Expected a integer flag with key '%v' but none was found; this is an error in Kurtosis!", enclavePoolSizeFlagKey)
	}

	if err := common.ValidateEnclavePoolSizeFlag(enclavePoolSize); err != nil {
		return stacktrace.Propagate(err, "An error occurred validating the '%v' flag", enclavePoolSizeFlagKey)
	}

	logrus.Infof("Restarting Kurtosis engine...")

	logLevelStr, err := flags.GetString(logLevelFlagKey)
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred while getting the Kurtosis engine log level using flag with key '%v'; this is a bug in Kurtosis", logLevelFlagKey)
	}

	logLevel, err := logrus.ParseLevel(logLevelStr)
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred parsing log level string '%v'", logLevelStr)
	}

	githubAuthTokenOverride, err := flags.GetString(githubAuthTokenOverrideFlagKey)
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred getting GitHub auth token override flag with key '%v'. This is a bug in Kurtosis", githubAuthTokenOverrideFlagKey)
	}

	engineManager, err := engine_manager.NewEngineManager(ctx)
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred creating an engine manager.")
	}

	engineVersion, err := flags.GetString(engineVersionFlagKey)
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred while getting the Kurtosis engine Container Version using flag with key '%v'; this is a bug in Kurtosis", engineVersionFlagKey)
	}

	isDebugMode, err := flags.GetBool(defaults.DebugModeFlagKey)
	if err != nil {
		return stacktrace.Propagate(err, "Expected a value for the '%v' flag but failed to get it", defaults.DebugModeFlagKey)
	}

	shouldStartInDebugMode := defaults.DefaultEnableDebugMode
	if isDebugMode {
		engineVersion = fmt.Sprintf("%s-%s", kurtosis_version.KurtosisVersion, defaults.DefaultKurtosisContainerDebugImageNameSuffix)
		shouldStartInDebugMode = true
	}

	shouldRestartAPIContainers, err := flags.GetBool(restartAPIContainersFlagKey)
	if err != nil {
		return stacktrace.Propagate(err, "Expected a value for the '%v' flag but failed to get it", restartAPIContainersFlagKey)
	}

	engineAuthorFlag, err := flags.GetString(engineAuthorFlagKey)
	if err != nil {
		return stacktrace.Propagate(err, "Expected a value for the '%v' flag but failed to get it", engineAuthorFlagKey)
	}

	var engineClientCloseFunc func() error
	var restartEngineErr error
	_, engineClientCloseFunc, restartEngineErr = engineManager.RestartEngineIdempotently(ctx, logLevel, engineVersion, engineAuthor, restartEngineOnSameVersionIfAnyRunning, enclavePoolSize, shouldStartInDebugMode, githubAuthTokenOverride, shouldRestartAPIContainers)
	if restartEngineErr != nil {
		return stacktrace.Propagate(restartEngineErr, "An error occurred restarting the Kurtosis engine")
	}
	defer func() {
		if err = engineClientCloseFunc(); err != nil {
			logrus.Warnf("Error closing the engine client:\n'%v'", err)
		}
	}()

	logrus.Infof("Engine restarted successfully")
	return nil
}
