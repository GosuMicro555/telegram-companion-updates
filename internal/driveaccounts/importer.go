package driveaccounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/session/tdesktop"
	"telegram-companion/internal/domain"
	appcrypto "telegram-companion/internal/service/crypto"
)

type Candidate struct {
	Account     domain.Account
	Credentials appcrypto.AppCredentials
	Session     *session.Data
	UserID      uint64
}
type ImportStore interface {
	ListAccounts(context.Context) ([]domain.Account, error)
	CommitDriveAccounts(context.Context, []Candidate) error
}
type Importer struct {
	Downloader  *Downloader
	Store       ImportStore
	SessionRoot string
	ScratchRoot string
	Defaults    appcrypto.AppCredentials
	Verify      func(context.Context, *Candidate) error
	AfterCommit func([]Candidate)
}

func (i *Importer) Process(ctx context.Context, ref Reference, phase func(string)) (Result, error) {
	result := Result{}
	if i.Store == nil || i.Downloader == nil || i.Verify == nil {
		return result, errors.New("import_unavailable")
	}
	scratch, err := privateRoot(i.ScratchRoot)
	if err != nil {
		return result, err
	}
	defer scratch.Close()
	temp, err := os.MkdirTemp(i.ScratchRoot, "batch-")
	if err != nil {
		return result, err
	}
	defer scratch.RemoveAll(filepath.Base(temp))
	if err = i.Downloader.Fetch(ctx, ref, temp, phase); err != nil {
		return result, err
	}
	phase("checking")
	candidates, err := readCandidates(ctx, temp, i.Defaults)
	if err != nil {
		return result, err
	}
	existing, err := i.Store.ListAccounts(ctx)
	if err != nil {
		return result, errors.New("storage_failed")
	}
	ids := map[domain.ID]bool{}
	keys := map[string]bool{}
	for _, account := range existing {
		ids[account.ID] = true
		loader := session.Loader{Storage: &session.FileStorage{Path: account.SessionPath}}
		data, e := loader.Load(ctx)
		if e != nil {
			return result, errors.New("existing_session_unreadable")
		}
		keys[sessionKey(data)] = true
	}
	pending := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		key := sessionKey(c.Session)
		if ids[c.Account.ID] || keys[key] {
			result.Skipped++
			continue
		}
		ids[c.Account.ID] = true
		keys[key] = true
		pending = append(pending, c)
	}
	if len(pending) == 0 {
		return result, nil
	}
	sessions, err := privateRoot(i.SessionRoot)
	if err != nil {
		return result, err
	}
	defer sessions.Close()
	directory, err := os.MkdirTemp(i.SessionRoot, "import-")
	if err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = sessions.RemoveAll(filepath.Base(directory))
		}
	}()
	for n := range pending {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		c := &pending[n]
		c.Account.SessionPath = filepath.Join(directory, fmt.Sprintf("%d.json", n))
		f, e := os.OpenFile(c.Account.SessionPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return result, e
		}
		if e = f.Close(); e != nil {
			return result, e
		}
		loader := session.Loader{Storage: &session.FileStorage{Path: c.Account.SessionPath}}
		if err = loader.Save(ctx, c.Session); err != nil {
			return result, err
		}
		phase("verifying")
		verifyCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err = i.Verify(verifyCtx, c)
		cancel()
		if err != nil {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	phase("saving")
	// Once committing, finish the small transaction even if the user presses Cancel.
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err = i.Store.CommitDriveAccounts(commitCtx, pending); err != nil {
		return result, errors.New("storage_failed")
	}
	committed = true
	result.Added = len(pending)
	if i.AfterCommit != nil {
		i.AfterCommit(pending)
	}
	return result, nil
}
func privateRoot(name string) (*os.Root, error) {
	if name == "" {
		return nil, ErrUnsafe
	}
	if err := os.MkdirAll(name, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnsafe
	}
	return os.OpenRoot(name)
}
func sessionKey(data *session.Data) string {
	h := sha256.Sum256(data.AuthKey)
	return hex.EncodeToString(h[:])
}
func convertAccount(a tdesktop.Account, credentials appcrypto.AppCredentials) (Candidate, error) {
	if a.Authorization.UserID == 0 {
		return Candidate{}, errors.New("tdata_invalid")
	}
	data, err := session.TDesktopSession(a)
	if err != nil || len(data.AuthKey) != 256 {
		return Candidate{}, errors.New("tdata_invalid")
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("telegram-user:%d", a.Authorization.UserID)))
	id := domain.ID("tdata-" + hex.EncodeToString(digest[:]))
	now := time.Now().UTC()
	return Candidate{Account: domain.Account{ID: id, Role: domain.AccountRoleSpammer, Status: domain.AccountStopped, ProxyMode: domain.ProxyModeGlobal, CreatedAt: now, UpdatedAt: now}, Credentials: credentials, Session: data, UserID: a.Authorization.UserID}, nil
}
func discoverRoots(ctx context.Context, base string) ([]string, error) {
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	roots := map[string]bool{}
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > MaxEntries {
			return ErrLimit
		}
		if name != "." && !safePath(name) {
			return ErrUnsafe
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrUnsafe
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return ErrUnsafe
		}
		if !entry.IsDir() && (entry.Name() == "key_datas" || entry.Name() == "key_data" || entry.Name() == "key_data0" || entry.Name() == "key_data1") {
			roots[path.Dir(name)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := []string{}
	for name := range roots {
		result = append(result, name)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return nil, errors.New("tdata_invalid")
	}
	return result, nil
}

// The library reads only through the bounded filesystem, preventing an oversized
// metadata file from being loaded into memory by the TData decoder.
type boundedFS struct{ fs.FS }

func (b boundedFS) Open(name string) (fs.File, error) {
	// Optional source config can override Telegram DC addresses. Use the library's
	// built-in production DC list instead of accepting endpoints from an archive.
	if strings.Contains(name, "/") {
		return nil, fs.ErrNotExist
	}
	f, e := b.FS.Open(name)
	if e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil || (!info.IsDir() && info.Size() > 16<<20) {
		f.Close()
		return nil, ErrLimit
	}
	return f, nil
}
func readCandidates(ctx context.Context, base string, defaults appcrypto.AppCredentials) (result []Candidate, err error) {
	// Invalid encrypted input must be an item failure, never a crashed application.
	defer func() {
		if recover() != nil {
			result = nil
			err = errors.New("tdata_invalid")
		}
	}()
	roots, err := discoverRoots(ctx, base)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, name := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		subtree, e := fs.Sub(root.FS(), name)
		if e != nil {
			return nil, errors.New("tdata_invalid")
		}
		creds, e := readCredentials(root.FS(), name, defaults)
		if e != nil {
			return nil, e
		}
		if e = validateTDataBounds(boundedFS{subtree}); e != nil {
			return nil, errors.New("tdata_invalid")
		}
		accounts, e := tdesktop.ReadFS(boundedFS{subtree}, nil)
		if e != nil {
			return nil, errors.New("tdata_invalid")
		}
		for _, a := range accounts {
			c, e := convertAccount(a, creds)
			if e != nil {
				return nil, e
			}
			result = append(result, c)
			if len(result) > 1000 {
				return nil, ErrLimit
			}
		}
	}
	if len(result) == 0 {
		return nil, errors.New("tdata_invalid")
	}
	return result, nil
}
func readCredentials(root fs.FS, name string, fallback appcrypto.AppCredentials) (appcrypto.AppCredentials, error) {
	found := false
	result := fallback
	dirs := []string{name}
	if name != "." {
		dirs = append(dirs, path.Dir(name))
	}
	for _, dir := range dirs {
		entries, err := fs.ReadDir(root, dir)
		if err != nil {
			return result, errors.New("tdata_invalid")
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
				continue
			}
			f, e := root.Open(path.Join(dir, entry.Name()))
			if e != nil {
				return result, errors.New("tdata_invalid")
			}
			data, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
			f.Close()
			if e != nil || len(data) > 1<<20 {
				return result, ErrLimit
			}
			var value struct {
				AppID   int    `json:"app_id"`
				AppHash string `json:"app_hash"`
				APIID   int    `json:"api_id"`
				APIHash string `json:"api_hash"`
			}
			e = json.Unmarshal(data, &value)
			clear(data)
			if e != nil {
				continue
			}
			if value.AppID == 0 {
				value.AppID = value.APIID
				value.AppHash = value.APIHash
			}
			if value.AppID == 0 && value.AppHash == "" {
				continue
			}
			c := appcrypto.AppCredentials{AppID: value.AppID, AppHash: value.AppHash}
			if c.AppID <= 0 || c.AppHash == "" || (found && c != result) {
				return result, errors.New("credentials_missing")
			}
			result = c
			found = true
		}
		if found {
			break
		}
	}
	if result.AppID <= 0 || result.AppHash == "" {
		return result, errors.New("credentials_missing")
	}
	return result, nil
}
