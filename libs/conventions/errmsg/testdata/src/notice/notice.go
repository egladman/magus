package notice

import (
	"context"
	"fmt"
	"log/slog"

	"notice/attr"
)

func notices(ctx context.Context, err error, name string) {
	slog.ErrorContext(ctx, err.Error(), attr.Notice(""))                                    // want `error-notice: an error in a notice's message`
	slog.WarnContext(ctx, fmt.Sprintf("could not open %s: %v", name, err), attr.Notice("")) // want `error-notice: an error in a notice's message`
	slog.Error("write: "+err.Error(), attr.Notice("lsp"))                                   // want `error-notice: an error in a notice's message`
	slog.Default().LogAttrs(ctx, slog.LevelWarn, err.Error(), attr.Notice(""))              // want `error-notice: an error in a notice's message`
	slog.ErrorContext(ctx, "could not open "+name, attr.Notice(""), attr.Error(err))
	slog.WarnContext(ctx, err.Error(), slog.String("k", "v"))
	slog.WarnContext(ctx, fmt.Sprintf("opened %s", name), attr.Notice(""))
}
