package logs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

type failure struct{}

func (failure) Error() string { return "failed" }

func messages(ctx context.Context, log *slog.Logger, err error, name string) {
	slog.Warn("could not open " + name + ": " + err.Error())                // want `error-log: an error in a log message`
	slog.ErrorContext(ctx, fmt.Sprintf("could not open %s: %v", name, err)) // want `error-log: an error in a log message`
	slog.InfoContext(ctx, err.Error())                                      // want `error-log: an error in a log message`
	log.Debug(fmt.Sprint("failed: ", errors.Unwrap(err)))                   // want `error-log: an error in a log message`
	log.WarnContext(ctx, "write: "+failure{}.Error(), "name", name)         // want `error-log: an error in a log message`
	slog.Default().LogAttrs(ctx, slog.LevelWarn, err.Error())               // want `error-log: an error in a log message`

	// The error rides an attribute, or the message names no error.
	slog.WarnContext(ctx, "could not open "+name, slog.Any("err", err))
	slog.InfoContext(ctx, fmt.Sprintf("opened %s", name))
	slog.Info("drained", "detail", err.Error())
	_ = fmt.Sprintf("could not open %s: %v", name, err)
}

func attributes(ctx context.Context, log *slog.Logger, err error, name string) {
	slog.WarnContext(ctx, "could not open", slog.String("error", err.Error())) // want `error-attr: an error's text in an "error" attribute`
	slog.WarnContext(ctx, "could not open", slog.String("err", err.Error()))   // want `error-attr: an error's text in an "error" attribute`
	log.Warn("could not open", "error", err.Error())                           // want `error-attr: an error's text in an "error" attribute`
	log.With(slog.Any("err", failure{}.Error())).Info("could not open")        // want `error-attr: an error's text in an "error" attribute`

	// Another key, a non-error value, or the error itself.
	slog.WarnContext(ctx, "could not open", slog.String("detail", err.Error()))
	slog.WarnContext(ctx, "could not open", slog.String("error", name))
	slog.WarnContext(ctx, "could not open", slog.Any("error", err))
	log.Warn("could not open", "error", err)
	log.Warn("could not open", "name", err.Error())
}
