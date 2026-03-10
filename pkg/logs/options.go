package logs

import "log/slog"

type Option func(*Options)

type Options struct {
	level       string
	format      string
	handler     slog.Handler
	addSource   bool
	replaceAttr func(groups []string, a slog.Attr) slog.Attr
}

func DefaultOptions() *Options {
	return &Options{
		level:  logLevel,
		format: format,
	}
}

func WithLevel(level string) Option {
	return func(o *Options) {
		o.level = level
	}
}

func WithFormat(format string) Option {
	return func(o *Options) {
		o.format = format
	}
}

func WithHandler(h slog.Handler) Option {
	return func(o *Options) {
		o.handler = h
	}
}

func WithAddSource(add bool) Option {
	return func(o *Options) {
		o.addSource = add
	}
}

func WithReplaceAttr(f func(groups []string, a slog.Attr) slog.Attr) Option {
	return func(o *Options) {
		o.replaceAttr = f
	}
}
