package imports

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	core "telegram-companion/internal/analytics"
	"telegram-companion/internal/analytics/ai"
	analytictext "telegram-companion/internal/analytics/text"
	"telegram-companion/internal/domain"
)

var (
	ErrRightsConfirmationRequired = errors.New("rights confirmation is required for AI import")
	ErrAIImportRequired           = errors.New("import service requires AI to be enabled")
	ErrSHA256Mismatch             = errors.New("import SHA-256 does not match")
	ErrUnsafeImportPath           = errors.New("import path must be a non-symlink regular file")
	ErrImportPathChanged          = errors.New("import path changed while creating snapshot")
)

type ImportRequest struct {
	Path            string
	SHA256          string
	RightsConfirmed bool
	UseAI           bool
	SourceKind      domain.SourceKind
	ProfileID       string
}

type Limits struct {
	MaxFileBytes      int64
	MaxRecordBytes    int
	MaxUniqueRecords  int
	EmbedBatchSize    int
	SnapshotDirectory string
}

var defaultLimits = Limits{MaxFileBytes: 256 << 20, MaxRecordBytes: 1 << 20, MaxUniqueRecords: 10_000, EmbedBatchSize: 32}

type Store interface {
	FindBySHA256(ctx context.Context, digest string) (*domain.Import, error)
	Profile(ctx context.Context, profileID string) (*core.Profile, error)
	Save(ctx context.Context, imported domain.Import, candidates []domain.ImportCandidate) error
}

type Option func(*ImportService)

type ImportService struct {
	store      Store
	provider   ai.EmbeddingProvider
	now        func() time.Time
	newID      func() string
	appVersion string
	thresholds domain.ImportThresholds
	limits     Limits
}

func NewImportService(store Store, provider ai.EmbeddingProvider, options ...Option) *ImportService {
	service := &ImportService{
		store: store, provider: provider, now: time.Now, newID: randomID, appVersion: "dev",
		thresholds: domain.ImportThresholds{SemanticWeight: 0.8, QualityWeight: 0.2}, limits: defaultLimits,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func WithClock(now func() time.Time) Option {
	return func(service *ImportService) { service.now = now }
}
func WithIDGenerator(newID func() string) Option {
	return func(service *ImportService) { service.newID = newID }
}
func WithAppVersion(version string) Option {
	return func(service *ImportService) { service.appVersion = version }
}
func WithLimits(limits Limits) Option {
	return func(service *ImportService) {
		if limits.MaxFileBytes > 0 {
			service.limits.MaxFileBytes = limits.MaxFileBytes
		}
		if limits.MaxRecordBytes > 0 {
			service.limits.MaxRecordBytes = limits.MaxRecordBytes
		}
		if limits.MaxUniqueRecords > 0 {
			service.limits.MaxUniqueRecords = limits.MaxUniqueRecords
		}
		if limits.EmbedBatchSize > 0 {
			service.limits.EmbedBatchSize = limits.EmbedBatchSize
		}
		if limits.SnapshotDirectory != "" {
			service.limits.SnapshotDirectory = limits.SnapshotDirectory
		}
	}
}

func (s *ImportService) Import(ctx context.Context, request ImportRequest) (domain.Import, error) {
	if err := s.validateDependencies(); err != nil {
		return domain.Import{}, err
	}
	if !request.RightsConfirmed {
		return domain.Import{}, ErrRightsConfirmationRequired
	}
	if !request.UseAI {
		return domain.Import{}, ErrAIImportRequired
	}
	sourceKind := request.SourceKind
	if sourceKind == "" {
		sourceKind = domain.SourceKindLocalImport
	}
	if sourceKind != domain.SourceKindLocalImport {
		return domain.Import{}, fmt.Errorf("%w: source_kind=%q", ai.ErrSourceKindNotAllowed, sourceKind)
	}
	snapshot, err := snapshotLocalFile(ctx, request.Path, s.limits, snapshotHooks{})
	if err != nil {
		return domain.Import{}, err
	}
	defer snapshot.cleanup()
	digest := snapshot.digest
	if !validSHA256(request.SHA256) || !strings.EqualFold(request.SHA256, digest) {
		return domain.Import{}, fmt.Errorf("%w: expected %s, calculated %s", ErrSHA256Mismatch, request.SHA256, digest)
	}
	existing, err := s.store.FindBySHA256(ctx, digest)
	if err != nil {
		return domain.Import{}, fmt.Errorf("check duplicate import: %w", err)
	}
	if existing != nil {
		return *existing, nil
	}
	profile := core.DefaultMoneyShortageProfile()
	if request.ProfileID != "" {
		profile, err = s.store.Profile(ctx, request.ProfileID)
		if err != nil {
			return domain.Import{}, fmt.Errorf("load import profile: %w", err)
		}
		if profile == nil {
			return domain.Import{}, errors.New("selected import profile was not found")
		}
	}
	records, inputCount, err := readRecords(ctx, snapshot.file, filepath.Ext(request.Path), s.limits)
	if err != nil {
		return domain.Import{}, err
	}
	candidates, metadata, err := s.rank(ctx, sourceKind, records, profile)
	if err != nil {
		return domain.Import{}, err
	}
	now := s.now().UTC()
	imported := domain.Import{
		ID: s.newID(), FileName: filepath.Base(request.Path), FilePath: request.Path, SHA256: digest,
		RightsConfirmed: request.RightsConfirmed, RightsConfirmedAt: now, ImportedAt: now,
		Status: domain.ImportStatusComplete, SourceKind: sourceKind, ProfileID: profile.ID,
		RecordCount: inputCount, Model: metadata, Thresholds: s.thresholds, AppVersion: s.appVersion,
	}
	if err := s.store.Save(ctx, imported, candidates); err != nil {
		return domain.Import{}, fmt.Errorf("save import: %w", err)
	}
	return imported, nil
}

func (s *ImportService) validateDependencies() error {
	if s == nil || s.store == nil || s.provider == nil || s.now == nil || s.newID == nil {
		return errors.New("import dependencies are required")
	}
	if s.limits.MaxFileBytes <= 0 || s.limits.MaxRecordBytes <= 0 || s.limits.MaxUniqueRecords <= 0 || s.limits.EmbedBatchSize <= 0 {
		return errors.New("import limits must be positive")
	}
	return nil
}

type recordAggregate struct {
	normalized, display string
	frequency           int
}

type recordCollector struct {
	limits  Limits
	records map[string]*recordAggregate
	count   int
}

func newRecordCollector(limits Limits) *recordCollector {
	return &recordCollector{limits: limits, records: make(map[string]*recordAggregate)}
}

func (c *recordCollector) add(value string) error {
	if !utf8.ValidString(value) {
		return errors.New("import record is not valid UTF-8")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) > c.limits.MaxRecordBytes {
		return fmt.Errorf("import record exceeds %d bytes", c.limits.MaxRecordBytes)
	}
	normalized := analytictext.Normalize(value)
	if normalized == "" {
		return nil
	}
	c.count++
	if existing := c.records[normalized]; existing != nil {
		existing.frequency++
		if value < existing.display {
			existing.display = value
		}
		return nil
	}
	if len(c.records) >= c.limits.MaxUniqueRecords {
		return fmt.Errorf("import exceeds %d unique records", c.limits.MaxUniqueRecords)
	}
	c.records[normalized] = &recordAggregate{normalized: normalized, display: value, frequency: 1}
	return nil
}

func (c *recordCollector) sorted() []recordAggregate {
	keys := make([]string, 0, len(c.records))
	for key := range c.records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]recordAggregate, 0, len(keys))
	for _, key := range keys {
		result = append(result, *c.records[key])
	}
	return result
}

func readRecords(ctx context.Context, reader io.Reader, extension string, limits Limits) ([]recordAggregate, int, error) {
	collector := newRecordCollector(limits)
	limited := io.LimitReader(reader, limits.MaxFileBytes+1)
	var err error
	switch strings.ToLower(extension) {
	case ".txt":
		err = parseTXT(ctx, limited, collector)
	case ".csv":
		err = parseCSV(ctx, limited, collector)
	case ".json":
		err = parseJSON(ctx, limited, collector)
	default:
		err = errors.New("only UTF-8 .txt, .csv, and .json imports are supported")
	}
	if err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	return collector.sorted(), collector.count, nil
}

func parseTXT(ctx context.Context, reader io.Reader, collector *recordCollector) error {
	buffered := newBoundedLineReader(ctx, reader, collector.limits.MaxRecordBytes)
	for {
		line, err := readBoundedLine(ctx, buffered, collector.limits.MaxRecordBytes)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream TXT import: %w", err)
		}
		if err := collector.add(string(line)); err != nil {
			return err
		}
	}
}

func parseCSV(ctx context.Context, reader io.Reader, collector *recordCollector) error {
	buffered := newBoundedLineReader(ctx, reader, collector.limits.MaxRecordBytes)
	textColumn := -1
	first := true
	for {
		line, err := readBoundedLine(ctx, buffered, collector.limits.MaxRecordBytes)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream CSV import: %w", err)
		}
		csvReader := csv.NewReader(bytes.NewReader(line))
		csvReader.FieldsPerRecord = -1
		record, err := csvReader.Read()
		if errors.Is(err, csv.ErrQuote) {
			return errors.New("multiline CSV records are not supported")
		}
		if err != nil {
			return fmt.Errorf("decode CSV record: %w", err)
		}
		if first {
			first = false
			for index, field := range record {
				switch strings.ToLower(strings.TrimSpace(field)) {
				case "text", "message", "content":
					textColumn = index
				}
			}
			if textColumn >= 0 {
				continue
			}
		}
		value := strings.Join(record, " ")
		if textColumn >= 0 {
			if textColumn >= len(record) {
				return errors.New("CSV text column is missing")
			}
			value = record[textColumn]
		}
		if err := collector.add(value); err != nil {
			return err
		}
	}
}

func newBoundedLineReader(ctx context.Context, reader io.Reader, max int) *bufio.Reader {
	return bufio.NewReaderSize(&contextReader{ctx: ctx, reader: reader}, max+2)
}

func readBoundedLine(ctx context.Context, reader *bufio.Reader, max int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, fmt.Errorf("record exceeds %d bytes", max)
	}
	if errors.Is(err, io.EOF) && len(line) == 0 {
		return nil, io.EOF
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	line = bytes.TrimSuffix(line, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) > max {
		return nil, fmt.Errorf("record exceeds %d bytes", max)
	}
	return line, nil
}

func parseJSON(ctx context.Context, reader io.Reader, collector *recordCollector) error {
	buffered := bufio.NewReaderSize(&contextReader{ctx: ctx, reader: reader}, 64<<10)
	first, err := readJSONNonSpace(ctx, buffered)
	if err != nil {
		return fmt.Errorf("decode JSON import: %w", err)
	}
	if first == '[' {
		return parseJSONArray(ctx, buffered, collector)
	}
	if err := buffered.UnreadByte(); err != nil {
		return fmt.Errorf("decode JSON import: %w", err)
	}
	for {
		raw, err := readBoundedJSONValue(ctx, buffered, collector.limits.MaxRecordBytes)
		if err != nil {
			return fmt.Errorf("decode JSON record: %w", err)
		}
		if err := collectJSONRecord(raw, collector); err != nil {
			return err
		}
		next, err := readJSONNonSpace(ctx, buffered)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("decode JSON import: %w", err)
		}
		if next == ',' || next == ']' {
			return errors.New("JSON stream contains array punctuation outside an array")
		}
		if err := buffered.UnreadByte(); err != nil {
			return fmt.Errorf("decode JSON import: %w", err)
		}
	}
}

func parseJSONArray(ctx context.Context, reader *bufio.Reader, collector *recordCollector) error {
	afterComma := false
	for {
		first, err := readJSONNonSpace(ctx, reader)
		if err != nil {
			return fmt.Errorf("decode JSON array: %w", err)
		}
		if first == ']' {
			if afterComma {
				return errors.New("JSON array has a trailing comma")
			}
			return ensureJSONWhitespaceEOF(ctx, reader)
		}
		if err := reader.UnreadByte(); err != nil {
			return err
		}
		raw, err := readBoundedJSONValue(ctx, reader, collector.limits.MaxRecordBytes)
		if err != nil {
			return fmt.Errorf("decode JSON record: %w", err)
		}
		if err := collectJSONRecord(raw, collector); err != nil {
			return err
		}
		delimiter, err := readJSONNonSpace(ctx, reader)
		if err != nil {
			return fmt.Errorf("decode JSON array delimiter: %w", err)
		}
		switch delimiter {
		case ',':
			afterComma = true
			continue
		case ']':
			return ensureJSONWhitespaceEOF(ctx, reader)
		default:
			return fmt.Errorf("invalid JSON array delimiter %q", delimiter)
		}
	}
}

func readBoundedJSONValue(ctx context.Context, reader *bufio.Reader, max int) ([]byte, error) {
	first, err := readJSONNonSpace(ctx, reader)
	if err != nil {
		return nil, err
	}
	if first != '{' && first != '"' {
		return nil, errors.New("JSON records must be strings or objects")
	}
	result := make([]byte, 0, min(max, 4096))
	result, err = appendBounded(result, first, max)
	if err != nil {
		return nil, err
	}
	depth := 0
	inString := first == '"'
	if first == '{' {
		depth = 1
	}
	escaped := false
	for {
		value, err := readJSONByte(ctx, reader)
		if err != nil {
			return nil, err
		}
		result, err = appendBounded(result, value, max)
		if err != nil {
			return nil, err
		}
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if value == '\\' {
				escaped = true
				continue
			}
			if value == '"' {
				inString = false
				if depth == 0 {
					return result, nil
				}
			}
			continue
		}
		switch value {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return result, nil
			}
		}
	}
}

func appendBounded(value []byte, next byte, limit int) ([]byte, error) {
	if len(value) >= limit {
		return nil, fmt.Errorf("JSON record exceeds %d bytes", limit)
	}
	if len(value) == cap(value) {
		capacity := min(limit, max(1, cap(value)*2))
		grown := make([]byte, len(value), capacity)
		copy(grown, value)
		value = grown
	}
	return append(value, next), nil
}

func collectJSONRecord(raw []byte, collector *recordCollector) error {
	value, err := jsonRecord(raw)
	if err != nil {
		return err
	}
	return collector.add(value)
}

func readJSONNonSpace(ctx context.Context, reader *bufio.Reader) (byte, error) {
	for {
		value, err := readJSONByte(ctx, reader)
		if err != nil {
			return 0, err
		}
		if value != ' ' && value != '\n' && value != '\r' && value != '\t' {
			return value, nil
		}
	}
}

func readJSONByte(ctx context.Context, reader *bufio.Reader) (byte, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return reader.ReadByte()
}

func ensureJSONWhitespaceEOF(ctx context.Context, reader *bufio.Reader) error {
	for {
		value, err := readJSONByte(ctx, reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if value != ' ' && value != '\n' && value != '\r' && value != '\t' {
			return errors.New("JSON import contains trailing data")
		}
	}
}

func jsonRecord(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", errors.New("JSON records must be strings or objects")
	}
	for _, key := range []string{"text", "message", "content"} {
		if field, ok := object[key]; ok {
			if err := json.Unmarshal(field, &value); err != nil {
				return "", fmt.Errorf("JSON %s field must be a string", key)
			}
			return value, nil
		}
	}
	return "", errors.New("JSON object requires text, message, or content")
}

func (s *ImportService) rank(ctx context.Context, sourceKind domain.SourceKind, records []recordAggregate, profile *core.Profile) ([]domain.ImportCandidate, domain.ModelMetadata, error) {
	if len(records) == 0 {
		return nil, domain.ModelMetadata{}, errors.New("import contains no usable records")
	}
	profileVectors, metadata, err := s.embedBatches(ctx, sourceKind, profile.PositiveExamples, domain.ModelMetadata{})
	if err != nil {
		return nil, domain.ModelMetadata{}, fmt.Errorf("embed profile examples: %w", err)
	}
	centroid, err := vectorCentroid(profileVectors)
	if err != nil {
		return nil, domain.ModelMetadata{}, err
	}
	texts := make([]string, len(records))
	for index := range records {
		if err := ctx.Err(); err != nil {
			return nil, domain.ModelMetadata{}, err
		}
		texts[index] = records[index].normalized
	}
	vectors, _, err := s.embedBatches(ctx, sourceKind, texts, metadata)
	if err != nil {
		return nil, domain.ModelMetadata{}, fmt.Errorf("embed imported records: %w", err)
	}
	candidates := make([]domain.ImportCandidate, 0, len(records))
	for index, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, domain.ModelMetadata{}, err
		}
		semantic, err := cosine(centroid, vectors[index])
		if err != nil {
			return nil, domain.ModelMetadata{}, err
		}
		lengthQuality := math.Min(1, float64(len([]rune(record.normalized)))/120)
		frequencyQuality := math.Min(1, math.Log1p(float64(record.frequency))/math.Log(10))
		quality := lengthQuality*0.7 + frequencyQuality*0.3
		score := semantic*s.thresholds.SemanticWeight + quality*s.thresholds.QualityWeight
		if score >= s.thresholds.MinimumScore {
			candidates = append(candidates, domain.ImportCandidate{NormalizedValue: record.normalized, DisplayValue: record.display, Frequency: record.frequency, SemanticScore: semantic, QualityScore: quality, Score: score})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].SemanticScore != candidates[j].SemanticScore {
			return candidates[i].SemanticScore > candidates[j].SemanticScore
		}
		if candidates[i].QualityScore != candidates[j].QualityScore {
			return candidates[i].QualityScore > candidates[j].QualityScore
		}
		return candidates[i].NormalizedValue < candidates[j].NormalizedValue
	})
	return candidates, metadata, nil
}

func (s *ImportService) embedBatches(ctx context.Context, sourceKind domain.SourceKind, texts []string, expected domain.ModelMetadata) ([][]float32, domain.ModelMetadata, error) {
	if len(texts) == 0 {
		return nil, domain.ModelMetadata{}, errors.New("embedding input is empty")
	}
	result := make([][]float32, 0, len(texts))
	metadata := expected
	for start := 0; start < len(texts); start += s.limits.EmbedBatchSize {
		if err := ctx.Err(); err != nil {
			return nil, domain.ModelMetadata{}, err
		}
		end := min(start+s.limits.EmbedBatchSize, len(texts))
		vectors, current, err := ai.EmbedLocal(ctx, s.provider, sourceKind, texts[start:end])
		if err != nil {
			return nil, domain.ModelMetadata{}, err
		}
		if err := ctx.Err(); err != nil {
			return nil, domain.ModelMetadata{}, err
		}
		if len(vectors) != end-start {
			return nil, domain.ModelMetadata{}, errors.New("embedding provider returned an unexpected vector count")
		}
		if metadata == (domain.ModelMetadata{}) {
			metadata = current
		} else if metadata != current {
			return nil, domain.ModelMetadata{}, errors.New("embedding provider metadata changed during import")
		}
		result = append(result, vectors...)
	}
	return result, metadata, nil
}

type localSnapshot struct {
	file    *os.File
	digest  string
	cleanup func()
}

type snapshotHooks struct {
	afterOpen func()
	afterCopy func()
}

func snapshotLocalFile(ctx context.Context, path string, limits Limits, hooks snapshotHooks) (_ *localSnapshot, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect local import: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, ErrUnsafeImportPath
	}
	if pathInfo.Size() > limits.MaxFileBytes {
		return nil, fmt.Errorf("import exceeds %d bytes", limits.MaxFileBytes)
	}

	source, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open local import: %w", err)
	}
	defer source.Close()
	openedInfo, err := source.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened import: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return nil, ErrImportPathChanged
	}
	if hooks.afterOpen != nil {
		hooks.afterOpen()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	snapshotDir, err := os.MkdirTemp(limits.SnapshotDirectory, ".telegram-companion-import-*")
	if err != nil {
		return nil, fmt.Errorf("create import snapshot directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(snapshotDir)
		}
	}()
	if err := os.Chmod(snapshotDir, 0o700); err != nil {
		return nil, fmt.Errorf("protect import snapshot directory: %w", err)
	}
	snapshotFile, err := os.OpenFile(filepath.Join(snapshotDir, "snapshot"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create import snapshot: %w", err)
	}
	defer func() {
		if !keep {
			_ = snapshotFile.Close()
		}
	}()

	hash := sha256.New()
	written, err := io.CopyBuffer(io.MultiWriter(snapshotFile, hash), io.LimitReader(&contextReader{ctx: ctx, reader: source}, limits.MaxFileBytes+1), make([]byte, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("copy import snapshot: %w", err)
	}
	if written > limits.MaxFileBytes {
		return nil, fmt.Errorf("import exceeds %d bytes", limits.MaxFileBytes)
	}
	if hooks.afterCopy != nil {
		hooks.afterCopy()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	afterInfo, err := source.Stat()
	if err != nil {
		return nil, fmt.Errorf("reinspect opened import: %w", err)
	}
	if !os.SameFile(openedInfo, afterInfo) || afterInfo.Size() != openedInfo.Size() || !afterInfo.ModTime().Equal(openedInfo.ModTime()) || written != openedInfo.Size() {
		return nil, ErrImportPathChanged
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || currentInfo.Mode()&os.ModeSymlink != 0 || !currentInfo.Mode().IsRegular() || !os.SameFile(openedInfo, currentInfo) {
		return nil, ErrImportPathChanged
	}
	if err := snapshotFile.Sync(); err != nil {
		return nil, fmt.Errorf("sync import snapshot: %w", err)
	}
	if _, err := snapshotFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind import snapshot: %w", err)
	}
	keep = true
	return &localSnapshot{
		file: snapshotFile, digest: hex.EncodeToString(hash.Sum(nil)),
		cleanup: func() { _ = snapshotFile.Close(); _ = os.RemoveAll(snapshotDir) },
	}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func vectorCentroid(vectors [][]float32) ([]float32, error) {
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, errors.New("profile embeddings are empty")
	}
	result := make([]float32, len(vectors[0]))
	for _, vector := range vectors {
		if len(vector) != len(result) {
			return nil, errors.New("embedding dimensions do not match")
		}
		for index, value := range vector {
			result[index] += value
		}
	}
	for index := range result {
		result[index] /= float32(len(vectors))
	}
	return result, nil
}

func cosine(left, right []float32) (float64, error) {
	if len(left) == 0 || len(left) != len(right) {
		return 0, errors.New("embedding dimensions do not match")
	}
	var dot, leftNorm, rightNorm float64
	for index := range left {
		l, r := float64(left[index]), float64(right[index])
		dot += l * r
		leftNorm += l * l
		rightNorm += r * r
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0, nil
	}
	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm)), nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func randomID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}
