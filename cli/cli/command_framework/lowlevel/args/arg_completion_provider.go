package args

import (
	"context"
	"github.com/kurtosis-tech/kurtosis/cli/cli/command_framework/lowlevel/flags"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/spf13/cobra"
)

const (
	shellDirectiveForManualCompletionProvider = cobra.ShellCompDirectiveNoFileComp
	shellDirectiveForShellProvideDefaultFileCompletion = cobra.ShellCompDirectiveDefault
	defaultShellDirective = cobra.ShellCompDirectiveDefault
	noShellDirectiveDefined = 999999
)

type argCompletionProvider interface {
	// Returns an argument completion func
	RunCompletionFunction(ctx context.Context, flags *flags.ParsedFlags, previousArgs *ParsedArgs) ([]string, cobra.ShellCompDirective, error)
}

// Only ONE of these fields will be set at a time!
type argCompletionProviderImpl struct {
	customCompletionFunc func(ctx context.Context, flags *flags.ParsedFlags, previousArgs *ParsedArgs) ([]string, error)

	shellCompletionDirective cobra.ShellCompDirective
}

func (impl *argCompletionProviderImpl) RunCompletionFunction(
	ctx context.Context,
	flags *flags.ParsedFlags,
	previousArgs *ParsedArgs,
)([]string, cobra.ShellCompDirective, error) {

	if impl.customCompletionFunc != nil {
		completions, err := impl.customCompletionFunc(ctx, flags, previousArgs)
		return completions, shellDirectiveForManualCompletionProvider, err
	}

	if impl.shellCompletionDirective != noShellDirectiveDefined {
		return nil, shellDirectiveForShellProvideDefaultFileCompletion, nil
	}

	return nil, defaultShellDirective, stacktrace.NewError("The custom completion func and the shell completion directive are not defined, this should never happens; this is a bug in Kurtosis")
}

//Receive a custom completion function which wi
func NewManualCompletionsProvider(
	customCompletionFunc func(ctx context.Context, flags *flags.ParsedFlags, previousArgs *ParsedArgs) ([]string, error),
) argCompletionProvider {
	newManualCompletionProvider := &argCompletionProviderImpl{
		customCompletionFunc: customCompletionFunc,
		shellCompletionDirective: noShellDirectiveDefined,
	}
	return newManualCompletionProvider
}

func NewDefaultFileCompletionProvider() argCompletionProvider {
	newDefaultFileCompletionProvider := &argCompletionProviderImpl{
		customCompletionFunc: nil,
		shellCompletionDirective: shellDirectiveForShellProvideDefaultFileCompletion,
	}
	return newDefaultFileCompletionProvider
}
