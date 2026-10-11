package attr

import "log/slog"

func Notice(label string) slog.Attr { return slog.String("notice", label) }

func Error(err error) slog.Attr { return slog.Any("error", err) }
