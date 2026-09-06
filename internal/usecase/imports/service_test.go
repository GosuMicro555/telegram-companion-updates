package imports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	core "telegram-companion/internal/analytics"
	"telegram-companion/internal/analytics/ai"
	"telegram-companion/internal/domain"

	"github.com/stretchr/testify/require"
)

func TestImportRejectsAIWithoutRightsConfirmation(t *testing.T) {
	fixturePath, digest := writeFixture(t, "one\ntwo\n")
	provider := &fakeProvider{}
	service := NewImportService(&fakeStore{}, provider)

	_, err := service.Import(context.Background(), ImportRequest{Path: fixturePath, SHA256: digest, RightsConfirmed: false, UseAI: true})

	require.ErrorIs(t, err, ErrRightsConfirmationRequired)
	require.Zero(t, provider.Calls())
}

func TestImportNeverCallsAIWithoutRightsEvenWhenUseAIIsFalse(t *testing.T) {
	fixturePath, digest := writeFixture(t, "one\n")
	provider := &fakeProvider{}
	service := NewImportService(&fakeStore{}, provider)

	_, err := service.Import(context.Background(), ImportRequest{Path: fixturePath, SHA256: digest, RightsConfirmed: false})

	require.ErrorIs(t, err, ErrRightsConfirmationRequired)
	require.Zero(t, provider.Calls())
}

func TestImportRejectsNonAIRequestWithoutProviderCall(t *testing.T) {
	fixturePath, digest := writeFixture(t, "one\n")
	provider := &fakeProvider{}
	service := NewImportService(&fakeStore{}, provider)

	_, err := service.Import(context.Background(), ImportRequest{Path: fixturePath, SHA256: digest, RightsConfirmed: true, UseAI: false})

	require.ErrorIs(t, err, ErrAIImportRequired)
	require.Zero(t, provider.Calls())
}

func TestImportRejectsTelegramBeforeProviderCall(t *testing.T) {
	fixturePath, digest := writeFixture(t, "one\n")
	provider := &fakeProvider{}
	service := NewImportService(&fakeStore{}, provider)

	_, err := service.Import(context.Background(), ImportRequest{Path: fixturePath, SHA256: digest, RightsConfirmed: true, UseAI: true, SourceKind: domain.SourceKindTelegram})

	require.ErrorIs(t, err, ai.ErrSourceKindNotAllowed)
	require.Zero(t, provider.Calls())
}

func TestImportVerifiesSHAAndDeduplicatesBeforeProviderCall(t *testing.T) {
	fixturePath, digest := writeFixture(t, "one\n")
	existing := domain.Import{ID: "existing", SHA256: digest, Status: domain.ImportStatusComplete}
	store := &fakeStore{existing: &existing}
	provider := &fakeProvider{}
	service := NewImportService(store, provider)

	got, err := service.Import(context.Background(), ImportRequest{Path: fixturePath, SHA256: digest, RightsConfirmed: true, UseAI: true})
	require.NoError(t, err)
	require.Equal(t, existing, got)
	require.Zero(t, provider.Calls())

	_, err = service.Import(context.Background(), ImportRequest{Path: fixturePath, SHA256: string(make([]byte, 64)), RightsConfirmed: true, UseAI: true})
	require.ErrorIs(t, err, ErrSHA256Mismatch)
	require.Zero(t, provider.Calls())
}

func TestSnapshotRejectsInitialSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	require.NoError(t, os.WriteFile(target, []byte("trusted\n"), 0o600))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := snapshotLocalFile(context.Background(), link, Limits{MaxFileBytes: 1024, SnapshotDirectory: dir}, snapshotHooks{})

	require.ErrorIs(t, err, ErrUnsafeImportPath)
}

func TestSnapshotRejectsPathReplacementAfterOpenAndCleansSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "records.txt")
	require.NoError(t, os.WriteFile(path, []byte("trusted\n"), 0o600))

	_, err := snapshotLocalFile(context.Background(), path, Limits{MaxFileBytes: 1024, SnapshotDirectory: dir}, snapshotHooks{
		afterOpen: func() {
			require.NoError(t, os.Rename(path, filepath.Join(dir, "opened.txt")))
			require.NoError(t, os.WriteFile(path, []byte("replacement\n"), 0o600))
		},
	})

	require.ErrorIs(t, err, ErrImportPathChanged)
	requireNoSnapshotArtifacts(t, dir)
}

func TestSnapshotRejectsSymlinkReplacementAfterOpenAndCleansSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "records.txt")
	target := filepath.Join(dir, "target.txt")
	require.NoError(t, os.WriteFile(path, []byte("trusted\n"), 0o600))
	require.NoError(t, os.WriteFile(target, []byte("other\n"), 0o600))
	var symlinkErr error

	_, err := snapshotLocalFile(context.Background(), path, Limits{MaxFileBytes: 1024, SnapshotDirectory: dir}, snapshotHooks{
		afterOpen: func() {
			require.NoError(t, os.Rename(path, filepath.Join(dir, "opened.txt")))
			symlinkErr = os.Symlink(target, path)
		},
	})
	if symlinkErr != nil {
		t.Skipf("symlink unavailable: %v", symlinkErr)
	}

	require.ErrorIs(t, err, ErrImportPathChanged)
	requireNoSnapshotArtifacts(t, dir)
}

func TestSnapshotHonorsCancellationAndCleansSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "records.txt")
	require.NoError(t, os.WriteFile(path, []byte("trusted\n"), 0o600))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := snapshotLocalFile(ctx, path, Limits{MaxFileBytes: 1024, SnapshotDirectory: dir}, snapshotHooks{})

	require.ErrorIs(t, err, context.Canceled)
	requireNoSnapshotArtifacts(t, dir)
}

func TestSnapshotCancellationAfterCopyCleansSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "records.txt")
	require.NoError(t, os.WriteFile(path, []byte("trusted\n"), 0o600))
	ctx, cancel := context.WithCancel(context.Background())

	_, err := snapshotLocalFile(ctx, path, Limits{MaxFileBytes: 1024, SnapshotDirectory: dir}, snapshotHooks{afterCopy: cancel})

	require.ErrorIs(t, err, context.Canceled)
	requireNoSnapshotArtifacts(t, dir)
}

func requireNoSnapshotArtifacts(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".telegram-companion-import-"), entry.Name())
	}
}

func TestImportStoresRightsAndModelProvenance(t *testing.T) {
	fixturePath, digest := writeFixture(t, "cannot pay rent\nneed money\n")
	now := time.Date(2026, 7, 11, 9, 30, 0, 0, time.UTC)
	store := &fakeStore{profile: &core.Profile{ID: "p1", Name: "Money", PositiveExamples: []string{"need money"}}}
	provider := &fakeProvider{metadata: ai.ModelMetadata{Name: "tiny", Repository: "fixture/tiny", Revision: "rev1", ModelSHA256: "model-hash", TokenizerSHA256: "tokenizer-hash"}}
	service := NewImportService(store, provider, WithClock(func() time.Time { return now }), WithIDGenerator(func() string { return "import-1" }), WithAppVersion("test-version"))

	got, err := service.Import(context.Background(), ImportRequest{Path: fixturePath, SHA256: digest, RightsConfirmed: true, UseAI: true, ProfileID: "p1"})

	require.NoError(t, err)
	require.Equal(t, "import-1", got.ID)
	require.True(t, got.RightsConfirmed)
	require.Equal(t, now, got.RightsConfirmedAt)
	require.Equal(t, digest, got.SHA256)
	require.Equal(t, "rev1", got.Model.Revision)
	require.Equal(t, "test-version", got.AppVersion)
	require.Equal(t, got, store.saved)
	require.NotEmpty(t, store.candidates)
}

func TestImportStreamsUTF8TXTCSVAndJSON(t *testing.T) {
	for _, test := range []struct {
		name, extension, contents string
	}{
		{"txt", ".txt", "нет денег\nслишком дорого\n"},
		{"csv", ".csv", "id,text\n1,нет денег\n2,слишком дорого\n"},
		{"json", ".json", `[{"text":"нет денег"},{"message":"слишком дорого"}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, digest := writeFixtureExt(t, test.extension, []byte(test.contents))
			store := &fakeStore{}
			provider := &fakeProvider{metadata: tinyMetadata()}
			service := NewImportService(store, provider, WithIDGenerator(func() string { return test.name }))

			got, err := service.Import(context.Background(), ImportRequest{Path: path, SHA256: digest, RightsConfirmed: true, UseAI: true})

			require.NoError(t, err)
			require.Equal(t, 2, got.RecordCount)
			require.Len(t, store.candidates, 2)
		})
	}
}

func TestImportSupportsJSONLinesAndSingleJSON(t *testing.T) {
	for _, test := range []struct{ name, extension, contents string }{
		{"json lines", ".json", "{\"text\":\"cannot pay rent\"}\n{\"content\":\"need money\"}\n"},
		{"single json", ".json", "{\"message\":\"need money\"}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, digest := writeFixtureExt(t, test.extension, []byte(test.contents))
			store := &fakeStore{}
			service := NewImportService(store, &fakeProvider{metadata: tinyMetadata()})
			got, err := service.Import(context.Background(), ImportRequest{Path: path, SHA256: digest, RightsConfirmed: true, UseAI: true})
			require.NoError(t, err)
			if test.name == "single json" {
				require.Equal(t, 1, got.RecordCount)
			} else {
				require.Equal(t, 2, got.RecordCount)
			}
		})
	}
}

func TestImportExplicitlyRejectsMultilineCSVBeforeProvider(t *testing.T) {
	path, digest := writeFixtureExt(t, ".csv", []byte("id,text\n1,\"cannot pay\nrent\"\n"))
	provider := &fakeProvider{metadata: tinyMetadata()}
	service := NewImportService(&fakeStore{}, provider)

	_, err := service.Import(context.Background(), ImportRequest{Path: path, SHA256: digest, RightsConfirmed: true, UseAI: true})

	require.Error(t, err)
	require.Contains(t, err.Error(), "multiline CSV")
	require.Zero(t, provider.Calls())
}

func TestCSVAndJSONRejectSimulated256MiBRecordBeforeAllocation(t *testing.T) {
	const simulatedSize = int64(256 << 20)
	for _, test := range []struct {
		name           string
		parse          func(context.Context, io.Reader, *recordCollector) error
		prefix, suffix string
	}{
		{"csv", parseCSV, "id,text\n1,", "\n"},
		{"json", parseJSON, `{"text":"`, `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := io.MultiReader(strings.NewReader(test.prefix), &repeatingByteReader{remaining: simulatedSize, value: 'a'}, strings.NewReader(test.suffix))
			counted := &countingReader{reader: stream}
			collector := newRecordCollector(Limits{MaxRecordBytes: 1 << 20, MaxUniqueRecords: 10})

			err := test.parse(context.Background(), counted, collector)

			require.Error(t, err)
			require.Contains(t, err.Error(), "exceeds")
			require.Less(t, counted.read, int64(2<<20))
		})
	}
}

func TestJSONRejectsTrailingArrayComma(t *testing.T) {
	collector := newRecordCollector(Limits{MaxRecordBytes: 1024, MaxUniqueRecords: 10})

	err := parseJSON(context.Background(), strings.NewReader(`[{"text":"one"},]`), collector)

	require.Error(t, err)
	require.Contains(t, err.Error(), "trailing comma")
}

func TestParserCancellationBetweenRecords(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &chunkReader{chunks: [][]byte{[]byte("first\n"), []byte("second\n")}, beforeRead: func(index int) {
		if index == 1 {
			cancel()
		}
	}}
	collector := newRecordCollector(Limits{MaxRecordBytes: 128, MaxUniqueRecords: 10})

	err := parseTXT(ctx, reader, collector)

	require.ErrorIs(t, err, context.Canceled)
}

func TestEmbeddingCancellationBetweenBatchesStopsProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider := &cancelingProvider{cancel: cancel}
	service := NewImportService(&fakeStore{}, provider, WithLimits(Limits{EmbedBatchSize: 1}))

	_, _, err := service.embedBatches(ctx, domain.SourceKindLocalImport, []string{"one", "two", "three"}, domain.ModelMetadata{})

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, provider.calls)
}

func TestImportRejectsInvalidUTF8AndBoundViolationsBeforeProvider(t *testing.T) {
	for _, test := range []struct {
		name     string
		contents []byte
		limits   Limits
	}{
		{"invalid utf8", []byte{0xff, '\n'}, Limits{MaxRecordBytes: 32, MaxUniqueRecords: 4, EmbedBatchSize: 2}},
		{"record too large", []byte("123456789\n"), Limits{MaxRecordBytes: 8, MaxUniqueRecords: 4, EmbedBatchSize: 2}},
		{"too many unique", []byte("one\ntwo\nthree\n"), Limits{MaxRecordBytes: 32, MaxUniqueRecords: 2, EmbedBatchSize: 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, digest := writeFixtureExt(t, ".txt", test.contents)
			provider := &fakeProvider{metadata: tinyMetadata()}
			service := NewImportService(&fakeStore{}, provider, WithLimits(test.limits))

			_, err := service.Import(context.Background(), ImportRequest{Path: path, SHA256: digest, RightsConfirmed: true, UseAI: true})

			require.Error(t, err)
			require.Zero(t, provider.Calls())
		})
	}
}

func TestImportBatchesUniqueRecordsAndAggregatesFrequency(t *testing.T) {
	path, digest := writeFixtureExt(t, ".txt", []byte("need money\nneed money\ncannot pay\ntoo expensive\n"))
	store := &fakeStore{}
	provider := &fakeProvider{metadata: tinyMetadata()}
	service := NewImportService(store, provider, WithLimits(Limits{MaxRecordBytes: 128, MaxUniqueRecords: 8, EmbedBatchSize: 2}))

	_, err := service.Import(context.Background(), ImportRequest{Path: path, SHA256: digest, RightsConfirmed: true, UseAI: true})

	require.NoError(t, err)
	require.LessOrEqual(t, provider.MaxBatch(), 2)
	require.Len(t, store.candidates, 3)
	byValue := make(map[string]domain.ImportCandidate)
	for _, candidate := range store.candidates {
		byValue[candidate.NormalizedValue] = candidate
	}
	require.Equal(t, 2, byValue["need money"].Frequency)
}

func TestImportGoldenOrderingIsDeterministicForPinnedHashes(t *testing.T) {
	path, digest := writeFixtureExt(t, ".json", []byte(`["beta need","alpha need","gamma"]`))
	var orderings [][]string
	for range 2 {
		store := &fakeStore{}
		provider := &fakeProvider{metadata: tinyMetadata(), fixed: map[string][]float32{
			"не хватает денег": {1, 0}, "нет денег": {1, 0}, "не могу позволить": {1, 0}, "слишком дорого": {1, 0}, "нужны деньги": {1, 0},
			"alpha need": {1, 0}, "beta need": {1, 0}, "gamma": {0, 1},
		}}
		service := NewImportService(store, provider, WithIDGenerator(func() string { return "golden" }))
		_, err := service.Import(context.Background(), ImportRequest{Path: path, SHA256: digest, RightsConfirmed: true, UseAI: true})
		require.NoError(t, err)
		ordering := make([]string, len(store.candidates))
		for index, candidate := range store.candidates {
			ordering[index] = candidate.NormalizedValue
		}
		orderings = append(orderings, ordering)
	}
	require.Equal(t, []string{"alpha need", "beta need", "gamma"}, orderings[0])
	require.Equal(t, orderings[0], orderings[1])
}

type fakeProvider struct {
	mu       sync.Mutex
	calls    int
	maxBatch int
	metadata ai.ModelMetadata
	fixed    map[string][]float32
}

type cancelingProvider struct {
	calls  int
	cancel context.CancelFunc
}

func (p *cancelingProvider) Embed(context.Context, []string) ([][]float32, ai.ModelMetadata, error) {
	p.calls++
	p.cancel()
	return [][]float32{{1, 0}}, tinyMetadata(), nil
}

type repeatingByteReader struct {
	remaining int64
	value     byte
}

func (r *repeatingByteReader) Read(buffer []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(buffer)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for index := 0; index < n; index++ {
		buffer[index] = r.value
	}
	r.remaining -= int64(n)
	return n, nil
}

type countingReader struct {
	reader io.Reader
	read   int64
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.read += int64(n)
	return n, err
}

type chunkReader struct {
	chunks     [][]byte
	index      int
	beforeRead func(int)
}

func (r *chunkReader) Read(buffer []byte) (int, error) {
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	if r.beforeRead != nil {
		r.beforeRead(r.index)
	}
	chunk := r.chunks[r.index]
	r.index++
	return copy(buffer, chunk), nil
}

func (p *fakeProvider) Embed(_ context.Context, texts []string) ([][]float32, ai.ModelMetadata, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if len(texts) > p.maxBatch {
		p.maxBatch = len(texts)
	}
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		if fixed, ok := p.fixed[text]; ok {
			vectors[i] = append([]float32(nil), fixed...)
			continue
		}
		vectors[i] = []float32{float32(len(text)), 1}
	}
	return vectors, p.metadata, nil
}

func (p *fakeProvider) Calls() int    { p.mu.Lock(); defer p.mu.Unlock(); return p.calls }
func (p *fakeProvider) MaxBatch() int { p.mu.Lock(); defer p.mu.Unlock(); return p.maxBatch }

type fakeStore struct {
	existing   *domain.Import
	profile    *core.Profile
	saved      domain.Import
	candidates []domain.ImportCandidate
}

func (s *fakeStore) FindBySHA256(context.Context, string) (*domain.Import, error) {
	return s.existing, nil
}
func (s *fakeStore) Profile(context.Context, string) (*core.Profile, error) { return s.profile, nil }
func (s *fakeStore) Save(_ context.Context, imported domain.Import, candidates []domain.ImportCandidate) error {
	s.saved = imported
	s.candidates = append([]domain.ImportCandidate(nil), candidates...)
	return nil
}

func writeFixture(t *testing.T, contents string) (string, string) {
	t.Helper()
	return writeFixtureExt(t, ".txt", []byte(contents))
}

func writeFixtureExt(t *testing.T, extension string, contents []byte) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture"+extension)
	require.NoError(t, os.WriteFile(path, contents, 0o600))
	sum := sha256.Sum256(contents)
	return path, hex.EncodeToString(sum[:])
}

func tinyMetadata() ai.ModelMetadata {
	return ai.ModelMetadata{Name: "tiny", Repository: "fixture/tiny", Revision: "rev", ModelSHA256: fmt.Sprintf("%064d", 1), TokenizerSHA256: fmt.Sprintf("%064d", 2)}
}
