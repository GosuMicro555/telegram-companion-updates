package runtimeconfig

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

func TestPublishReplacesImmutableSnapshot(t *testing.T) {
	store := NewStore(Snapshot{SharedReply: "old"})
	next := store.Current()
	next.SharedReply = "  новый ответ\nс новой строкой  "
	rev := store.Publish(next)

	if rev != 2 {
		t.Fatalf("revision = %d, want 2", rev)
	}
	if got := store.Current().SharedReply; got != "  новый ответ\nс новой строкой  " {
		t.Fatalf("SharedReply = %q", got)
	}
}

func TestPublishRetainsSeparatePrivateReply(t *testing.T) {
	store := NewStore(Snapshot{SharedReply: "comment reply", PrivateReply: "private reply", PrivateReplyPresent: true})
	next := store.Current()
	next.SharedReply = "updated comment reply"
	next.PrivateReply = "updated private reply"
	store.Publish(next)

	got := store.Current()
	require.Equal(t, "updated comment reply", got.SharedReply)
	require.Equal(t, "updated private reply", got.PrivateReply)
	require.True(t, got.PrivateReplyPresent)
}

func TestCurrentReturnsDeepImmutableCopy(t *testing.T) {
	store := NewStore(Snapshot{
		Roles: map[domain.ID]domain.AccountRole{"account": domain.AccountRoleSpammer},
		CatalogAssignments: map[domain.SourceCatalog][]domain.ID{
			domain.SourceCatalogOutbound: {"channel"},
		},
		Keywords:              []string{"keyword"},
		MinusKeywords:         []string{"blocked phrase"},
		DirectMessageKeywords: []string{"keyword"},
		ProxyAssignments:      map[domain.ID]string{"account": "proxy"},
	})

	copy := store.Current()
	copy.Roles["account"] = domain.AccountRoleScoutAnalyst
	copy.CatalogAssignments[domain.SourceCatalogOutbound][0] = "changed"
	copy.Keywords[0] = "changed"
	copy.MinusKeywords[0] = "changed"
	copy.DirectMessageKeywords[0] = "changed"
	copy.ProxyAssignments["account"] = "changed"

	got := store.Current()
	if got.Roles["account"] != domain.AccountRoleSpammer {
		t.Fatalf("roles were mutated: %+v", got.Roles)
	}
	if got.CatalogAssignments[domain.SourceCatalogOutbound][0] != "channel" {
		t.Fatalf("catalog assignments were mutated: %+v", got.CatalogAssignments)
	}
	if got.Keywords[0] != "keyword" || got.MinusKeywords[0] != "blocked phrase" || got.DirectMessageKeywords[0] != "keyword" {
		t.Fatalf("keyword slices were mutated: %+v", got)
	}
	if got.ProxyAssignments["account"] != "proxy" {
		t.Fatalf("proxy assignments were mutated: %+v", got.ProxyAssignments)
	}
}

func TestUpdateClonesProxyAssignmentsBeforePublishing(t *testing.T) {
	store := NewStore(Snapshot{})
	assignments := map[domain.ID]string{"account": "route-a"}

	store.Update(func(next *Snapshot) {
		next.ProxyAssignments = assignments
	})
	assignments["account"] = "route-b"
	assignments["other-account"] = "route-c"

	got := store.Current().ProxyAssignments
	if got["account"] != "route-a" {
		t.Fatalf("account route = %q, want route-a", got["account"])
	}
	if _, exists := got["other-account"]; exists {
		t.Fatalf("proxy assignments were aliased: %+v", got)
	}
}

func TestSubscribeReplacesPendingSnapshotWithNewestRevision(t *testing.T) {
	store := NewStore(Snapshot{SharedReply: "one"})
	updates, unsubscribe := store.Subscribe(1)
	defer unsubscribe()

	next := store.Current()
	next.SharedReply = "two"
	store.Publish(next)
	next = store.Current()
	next.SharedReply = "three"
	store.Publish(next)

	got := <-updates
	if got.Revision != 3 || got.SharedReply != "three" {
		t.Fatalf("update = %+v, want revision 3 with newest reply", got)
	}
}

func TestPublishAndCurrentAreRaceSafe(t *testing.T) {
	store := NewStore(Snapshot{
		Roles: map[domain.ID]domain.AccountRole{"account": domain.AccountRoleSpammer},
		CatalogAssignments: map[domain.SourceCatalog][]domain.ID{
			domain.SourceCatalogOutbound: {"channel"},
		},
		Keywords:              []string{"keyword"},
		DirectMessageKeywords: []string{"keyword"},
		ProxyAssignments:      map[domain.ID]string{"account": "proxy"},
	})
	updates, unsubscribe := store.Subscribe(1)
	defer unsubscribe()

	var readers sync.WaitGroup
	for reader := 0; reader < 32; reader++ {
		readers.Add(1)
		go func(reader int) {
			defer readers.Done()
			for i := 0; i < 100; i++ {
				snapshot := store.Current()
				snapshot.Roles["account"] = domain.AccountRoleScoutAnalyst
				snapshot.CatalogAssignments[domain.SourceCatalogOutbound][0] = domain.ID(fmt.Sprintf("%d-%d", reader, i))
				snapshot.Keywords[0] = fmt.Sprintf("keyword-%d-%d", reader, i)
				snapshot.DirectMessageKeywords[0] = fmt.Sprintf("dm-%d-%d", reader, i)
				snapshot.ProxyAssignments["account"] = fmt.Sprintf("proxy-%d-%d", reader, i)
			}
		}(reader)
	}

	for i := 0; i < 100; i++ {
		next := store.Current()
		next.SharedReply = fmt.Sprintf("reply-%d", i)
		store.Publish(next)
	}
	readers.Wait()

	got := store.Current()
	if got.Revision != 101 || got.SharedReply != "reply-99" {
		t.Fatalf("current = %+v, want revision 101 and latest reply", got)
	}
	update := <-updates
	if update.Revision != 101 || update.SharedReply != "reply-99" {
		t.Fatalf("subscriber update = %+v, want latest snapshot", update)
	}
}

func TestSubscribeUnsubscribeClosesAndRemovesSubscriber(t *testing.T) {
	store := NewStore(Snapshot{SharedReply: "one"})
	updates, unsubscribe := store.Subscribe(1)
	unsubscribe()
	unsubscribe()

	_, open := <-updates
	if open {
		t.Fatal("subscriber channel remained open after unsubscribe")
	}
	store.Publish(Snapshot{SharedReply: "two"})
}

func TestUpdateSerializesConcurrentIndependentFieldMutations(t *testing.T) {
	store := NewStore(Snapshot{})
	start := make(chan struct{})
	var updates sync.WaitGroup

	updates.Add(2)
	go func() {
		defer updates.Done()
		<-start
		store.Update(func(next *Snapshot) {
			next.Keywords = []string{"keyword"}
		})
	}()
	go func() {
		defer updates.Done()
		<-start
		store.Update(func(next *Snapshot) {
			next.RateLimits = RateLimits{RepliesPerMinute: 7, MinIntervalSeconds: 3}
		})
	}()

	close(start)
	updates.Wait()

	got := store.Current()
	if len(got.Keywords) != 1 || got.Keywords[0] != "keyword" {
		t.Fatalf("keywords = %+v, want keyword", got.Keywords)
	}
	if got.RateLimits != (RateLimits{RepliesPerMinute: 7, MinIntervalSeconds: 3}) {
		t.Fatalf("rate limits = %+v, want %+v", got.RateLimits, RateLimits{RepliesPerMinute: 7, MinIntervalSeconds: 3})
	}
}

func TestStoreDeepCopiesCanonicalTriggers(t *testing.T) {
	triggers := []CanonicalTrigger{{ID: "money", Canonical: "деньги", Forms: []string{"деньги", "денег"}}}
	store := NewStore(Snapshot{CanonicalTriggers: triggers})

	triggers[0].Canonical = "changed"
	triggers[0].Forms[0] = "changed"
	got := store.Current()
	got.CanonicalTriggers[0].Canonical = "mutated"
	got.CanonicalTriggers[0].Forms[1] = "mutated"

	require.Equal(t, "деньги", store.Current().CanonicalTriggers[0].Canonical)
	require.Equal(t, []string{"деньги", "денег"}, store.Current().CanonicalTriggers[0].Forms)
}

func TestUpdateSerializesConcurrentProxyAssignmentMutations(t *testing.T) {
	store := NewStore(Snapshot{ProxyAssignments: map[domain.ID]string{}})
	start := make(chan struct{})
	var updates sync.WaitGroup

	for account, route := range map[domain.ID]string{
		"account-a": "route-a",
		"account-b": "route-b",
	} {
		updates.Add(1)
		go func(account domain.ID, route string) {
			defer updates.Done()
			<-start
			store.Update(func(next *Snapshot) {
				next.ProxyAssignments[account] = route
			})
		}(account, route)
	}

	close(start)
	updates.Wait()

	got := store.Current()
	if got.ProxyAssignments["account-a"] != "route-a" || got.ProxyAssignments["account-b"] != "route-b" {
		t.Fatalf("proxy assignments = %+v, want both route assignments", got.ProxyAssignments)
	}
	if got.Revision != 3 {
		t.Fatalf("revision = %d, want 3", got.Revision)
	}
}
