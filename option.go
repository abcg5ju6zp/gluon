package gluon

import (
	"crypto/tls"
	"io"
	"time"

	"github.com/ProtonMail/gluon/async"
	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/imap/connectioncounter"
	"github.com/ProtonMail/gluon/imap/connectionlimiter"
	"github.com/ProtonMail/gluon/internal/unleash"
	limits2 "github.com/ProtonMail/gluon/limits"
	"github.com/ProtonMail/gluon/observability"
	"github.com/ProtonMail/gluon/profiling"
	"github.com/ProtonMail/gluon/reporter"
	"github.com/ProtonMail/gluon/store"
	"github.com/ProtonMail/gluon/version"
)

// Option represents a type that can be used to configure the server.
type Option interface {
	config(*serverBuilder)
}

// WithDelimiter instructs the server to use the given path delimiter instead of the default ('/').
func WithDelimiter(delimiter string) Option {
	return &withDelimiter{
		delimiter: delimiter,
	}
}

type withDelimiter struct {
	delimiter string
}

func (opt withDelimiter) config(builder *serverBuilder) {
	builder.delim = opt.delimiter
}

// WithLoginJailTime instructs the server to use the given login jail time.
func WithLoginJailTime(loginJailTime time.Duration) Option {
	return &withLoginJailTime{
		loginJailTime: loginJailTime,
	}
}

func (opt withLoginJailTime) config(builder *serverBuilder) {
	builder.loginJailTime = opt.loginJailTime
}

type withLoginJailTime struct {
	loginJailTime time.Duration
}

// WithTLS instructs the server to use the given TLS config.
func WithTLS(cfg *tls.Config) Option {
	return &withTLS{
		cfg: cfg,
	}
}

type withTLS struct {
	cfg *tls.Config
}

func (opt withTLS) config(builder *serverBuilder) {
	builder.tlsConfig = opt.cfg
}

// WithIdleBulkTime instructs the server to use the given IDLE bulk time.
func WithIdleBulkTime(idleBulkTime time.Duration) Option {
	return &withIdleBulkTime{
		idleBulkTime: idleBulkTime,
	}
}

type withIdleBulkTime struct {
	idleBulkTime time.Duration
}

func (opt withIdleBulkTime) config(builder *serverBuilder) {
	builder.idleBulkTime = opt.idleBulkTime
}

// WithLogger instructs the server to write incoming and outgoing IMAP communication to the given io.Writers.
func WithLogger(in, out io.Writer) Option {
	return &withLogger{
		in:  in,
		out: out,
	}
}

type withLogger struct {
	in, out io.Writer
}

func (opt withLogger) config(builder *serverBuilder) {
	builder.inLogger = opt.in
	builder.outLogger = opt.out
}

type withVersionInfo struct {
	versionInfo version.Info
}

func (vi *withVersionInfo) config(builder *serverBuilder) {
	builder.versionInfo = vi.versionInfo
}

func WithVersionInfo(
	vmajor, vminor, vpatch int,
	name, vendor, supportURL string,
) Option {
	return &withVersionInfo{
		versionInfo: version.Info{
			Name: name,
			Version: version.Version{
				Major: vmajor,
				Minor: vminor,
				Patch: vpatch,
			},
			Vendor:     vendor,
			SupportURL: supportURL,
		},
	}
}

type withCmdExecProfiler struct {
	builder profiling.CmdProfilerBuilder
}

func (c *withCmdExecProfiler) config(builder *serverBuilder) {
	builder.cmdExecProfBuilder = c.builder
}

// WithCmdProfiler allows a specific CmdProfilerBuilder to be set for the server's execution.
func WithCmdProfiler(builder profiling.CmdProfilerBuilder) Option {
	return &withCmdExecProfiler{builder: builder}
}

type withStoreBuilder struct {
	builder store.Builder
}

func (w *withStoreBuilder) config(builder *serverBuilder) {
	builder.storeBuilder = w.builder
}

func WithStoreBuilder(builder store.Builder) Option {
	return &withStoreBuilder{builder: builder}
}

type withDataDir struct {
	path string
}

func (w *withDataDir) config(builder *serverBuilder) {
	builder.dataDir = w.path
}

func WithDataDir(path string) Option {
	return &withDataDir{path: path}
}

type withDatabaseDir struct {
	path string
}

func (w *withDatabaseDir) config(builder *serverBuilder) {
	builder.databaseDir = w.path
}

func WithDatabaseDir(path string) Option {
	return &withDatabaseDir{path: path}
}

type withReporter struct {
	reporter reporter.Reporter
}

func (w *withReporter) config(builder *serverBuilder) {
	builder.reporter = w.reporter
}

func WithReporter(reporter reporter.Reporter) Option {
	return &withReporter{reporter: reporter}
}

type withDisableParallelism struct{}

func (withDisableParallelism) config(builder *serverBuilder) {
	builder.disableParallelism = true
}

func WithDisableParallelism() Option {
	return &withDisableParallelism{}
}

type withPanicHandler struct {
	panicHandler async.PanicHandler
}

func (opt *withPanicHandler) config(builder *serverBuilder) {
	builder.panicHandler = opt.panicHandler
}

func WithPanicHandler(panicHandler async.PanicHandler) Option {
	return &withPanicHandler{panicHandler}
}

type withIMAPLimits struct {
	limits limits2.IMAP
}

func (w withIMAPLimits) config(builder *serverBuilder) {
	builder.imapLimits = w.limits
}

func WithIMAPLimits(limits limits2.IMAP) Option {
	return &withIMAPLimits{
		limits: limits,
	}
}

type withDisableIMAPAuthenticate struct{}

func (withDisableIMAPAuthenticate) config(builder *serverBuilder) {
	builder.disableIMAPAuthenticate = true
}

func WithDisableIMAPAuthenticate() Option {
	return &withDisableIMAPAuthenticate{}
}

type withUIDValidityGenerator struct {
	generator imap.UIDValidityGenerator
}

func (w withUIDValidityGenerator) config(builder *serverBuilder) {
	builder.uidValidityGenerator = w.generator
}

func WithUIDValidityGenerator(generator imap.UIDValidityGenerator) Option {
	return &withUIDValidityGenerator{generator: generator}
}

type withDBClient struct {
	ci db.ClientInterface
}

func (w withDBClient) config(builder *serverBuilder) {
	builder.dbCI = w.ci
}

func WithDBClient(ci db.ClientInterface) Option {
	return &withDBClient{ci: ci}
}

type withObservabilitySender struct {
	sender observability.Sender
}

func (w withObservabilitySender) config(builder *serverBuilder) {
	builder.observabilitySender = w.sender
}

func WithObservabilitySender(sender observability.Sender, imapErrorType, messageErrorType, otherErrorType int) Option {
	observability.SetupMetricTypes(imapErrorType, messageErrorType, otherErrorType)
	return &withObservabilitySender{sender: sender}
}

type withConnectionRollingCounter struct {
	rollingConnectionCounter *connectioncounter.RollingCounter
}

func (w withConnectionRollingCounter) config(builder *serverBuilder) {
	builder.connectionRollingCounter = w.rollingConnectionCounter
}

func WithConnectionRollingCounter(connectionLimitThreshold, observabilityThreshold, numberOfBuckets int, thresholdCheckInterval time.Duration) Option {
	return &withConnectionRollingCounter{
		rollingConnectionCounter: connectioncounter.NewRollingCounter(
			connectionLimitThreshold,
			observabilityThreshold,
			numberOfBuckets,
			thresholdCheckInterval,
		),
	}
}

type withFeatureFlagProvider struct {
	featureFlagProvider unleash.FeatureFlagValueProvider
}

func (w withFeatureFlagProvider) config(builder *serverBuilder) {
	builder.featureFlagProvider = w.featureFlagProvider
}

func WithFeatureFlagProvider(featureFlagProvider unleash.FeatureFlagValueProvider) Option {
	return &withFeatureFlagProvider{
		featureFlagProvider: featureFlagProvider,
	}
}

type withConnectionLimiter struct {
	limiter connectionlimiter.ConnectionLimiter
}

func (w withConnectionLimiter) config(builder *serverBuilder) {
	builder.connectionLimiter = w.limiter
}

func WithConnectionLimiter(limits, fallbackLimits connectionlimiter.Limits) Option {
	return &withConnectionLimiter{
		limiter: connectionlimiter.NewConnectionLimiter(limits, fallbackLimits),
	}
}

type withAppendReadTimeout struct {
	timeout time.Duration
}

func (w withAppendReadTimeout) config(builder *serverBuilder) {
	builder.appendReadTimeout = w.timeout
}

// WithAppendReadTimeout bounds how long the server waits for an APPEND literal body to
// arrive. Zero disables the timeout. Defaults to 5 minutes.
func WithAppendReadTimeout(timeout time.Duration) Option {
	return &withAppendReadTimeout{timeout: timeout}
}

type withAppendSpoolMemoryLimit struct {
	limit int64
}

func (w withAppendSpoolMemoryLimit) config(builder *serverBuilder) {
	builder.appendSpoolMemoryLimit = w.limit
}

// WithAppendSpoolMemoryLimit sets the maximum in-memory size of a streamed APPEND literal
// before it spills to a restricted temporary file.
func WithAppendSpoolMemoryLimit(limit int64) Option {
	return &withAppendSpoolMemoryLimit{limit: limit}
}

type withAppendJournalRetention struct {
	retention time.Duration
}

func (w withAppendJournalRetention) config(builder *serverBuilder) {
	builder.appendJournalRetention = w.retention
}

// WithAppendJournalRetention sets how long confirmed append journal entries are retained to
// make retried APPEND requests after a lost connection idempotent. Defaults to 24 hours.
func WithAppendJournalRetention(retention time.Duration) Option {
	return &withAppendJournalRetention{retention: retention}
}
