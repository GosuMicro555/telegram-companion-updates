package gotd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"telegram-companion/internal/domain"
)

const (
	membershipJoining         = "joining"
	membershipPendingApproval = "pending_approval"
	membershipMember          = "member"
	membershipLeaving         = "leaving"
)

type membershipJoinOutcome uint8

const (
	membershipAlreadyJoined membershipJoinOutcome = iota
	membershipNewlyJoined
)

type membershipTransition struct {
	membership domain.ChannelMembership
	peer       tg.InputPeerClass
	title      string
	changed    bool
}

func loadAccountMembershipIntent(ctx context.Context, memberships accountMembershipCatalog, accountID domain.ID, catalog domain.SourceCatalog, row domain.Channel) (domain.ChannelMembership, bool, error) {
	if memberships == nil {
		if isPrivateInviteLink(row.Link) {
			return domain.ChannelMembership{}, false, nil
		}
		return domain.ChannelMembership{AccountID: accountID, ChannelID: row.ID, IsMember: true, Status: membershipMember}, true, nil
	}
	membership, found, err := memberships.LoadMembership(ctx, accountID, catalog, row.ID)
	if err != nil {
		return domain.ChannelMembership{}, false, err
	}
	if !found {
		return domain.ChannelMembership{}, false, nil
	}
	if membership.Status == membershipJoining && membership.JoinNotBefore != nil && membership.JoinNotBefore.After(time.Now().UTC()) {
		return domain.ChannelMembership{}, false, nil
	}
	return membership, found, nil
}

func loadOrCreateAccountMembership(ctx context.Context, memberships accountMembershipCatalog, accountID domain.ID, catalog domain.SourceCatalog, row domain.Channel) (domain.ChannelMembership, bool, error) {
	membership, found, err := loadAccountMembershipIntent(ctx, memberships, accountID, catalog, row)
	if err != nil || !found {
		return domain.ChannelMembership{}, false, err
	}
	if !membership.IsMember && membership.Status != membershipJoining {
		return domain.ChannelMembership{}, false, nil
	}
	return membership, true, nil
}

func reconcileMembershipIntent(ctx context.Context, api *tg.Client, row domain.Channel, membership domain.ChannelMembership, checkedAt time.Time) (membershipTransition, error) {
	result := membershipTransition{membership: membership}
	switch membership.Status {
	case membershipJoining:
		if membership.JoinNotBefore != nil && membership.JoinNotBefore.After(checkedAt) {
			return result, nil
		}
		var outcome membershipJoinOutcome
		var err error
		if isPrivateInviteLink(row.Link) {
			result.peer, result.title, outcome, err = joinTelegramPeer(ctx, api, row.Link)
		} else {
			result.peer, result.title, outcome, err = joinPublicTelegramPeer(ctx, api, row.Link)
		}
		if err != nil {
			if !isInviteRequestSent(err) {
				return membershipTransition{}, err
			}
			result.membership.IsMember = false
			result.membership.Status = membershipPendingApproval
			result.membership.LastCheckAt = timePointer(checkedAt)
			if result.membership.RequestSubmittedAt == nil {
				result.membership.RequestSubmittedAt = timePointer(checkedAt)
			}
			result.membership.LastError = ""
			result.changed = true
			return result, nil
		}
		result.markMember(checkedAt, outcome)
		return result, nil
	case membershipPendingApproval:
		var peer tg.InputPeerClass
		var title string
		var already bool
		var err error
		if isPrivateInviteLink(row.Link) {
			peer, title, already, err = checkPrivateInviteMembership(ctx, api, row.Link)
		} else {
			peer, title, already, err = checkPublicMembership(ctx, api, row.Link)
		}
		if err != nil {
			return membershipTransition{}, err
		}
		result.membership.LastCheckAt = timePointer(checkedAt)
		result.membership.LastError = ""
		result.changed = true
		if !already {
			return result, nil
		}
		result.peer = peer
		result.title = title
		result.markMember(checkedAt, membershipNewlyJoined)
		return result, nil
	default:
		return result, nil
	}
}

type catalogRemovalCoordinator interface {
	DeleteMembership(context.Context, domain.SourceCatalog, domain.ID, domain.ID) error
	CompleteCatalogRemoval(context.Context, domain.SourceCatalog, domain.ID) error
}

type membershipIntentDeletionStore interface {
	DeleteMembership(context.Context, domain.SourceCatalog, domain.ID, domain.ID) error
}

func reconcileRemovalMembership(ctx context.Context, api *tg.Client, row domain.Channel, membership domain.ChannelMembership, checkedAt time.Time) (bool, error) {
	if membership.Status == membershipPendingApproval {
		return false, nil
	}
	if !membership.IsMember {
		return true, nil
	}
	if api == nil {
		return false, errors.New("telegram API is unavailable for channel removal")
	}
	parent, title, err := resolveTelegramPeer(ctx, api, row.Link, false)
	if err != nil {
		return false, err
	}
	peer, _, err := resolveExplicitDiscussionTarget(ctx, api, row.Link, parent, title)
	if err != nil {
		return false, err
	}
	if row.TelegramID != "" && inputPeerID(peer) != row.TelegramID {
		return false, fmt.Errorf("resolved peer %s does not match stored channel peer %s", inputPeerID(peer), row.TelegramID)
	}
	switch value := peer.(type) {
	case *tg.InputPeerChannel:
		_, err = api.ChannelsLeaveChannel(ctx, &tg.InputChannel{ChannelID: value.ChannelID, AccessHash: value.AccessHash})
	case *tg.InputPeerChat:
		_, err = api.MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{
			ChatID: value.ChatID, UserID: &tg.InputUserSelf{},
		})
	default:
		return false, fmt.Errorf("unsupported peer type %T for channel removal", peer)
	}
	if err != nil && !tgerr.Is(err, "USER_NOT_PARTICIPANT") {
		return false, err
	}
	_ = checkedAt
	return true, nil
}

func reconcileLeavingMembership(ctx context.Context, api *tg.Client, row domain.Channel, membership domain.ChannelMembership, checkedAt time.Time) (membershipTransition, bool, error) {
	result := membershipTransition{membership: membership}
	if !result.membership.IsMember {
		if result.membership.RequestSubmittedAt == nil {
			return result, true, nil
		}
		pending := result.membership
		pending.Status = membershipPendingApproval
		transition, err := reconcileMembershipIntent(ctx, api, row, pending, checkedAt)
		if err != nil {
			return membershipTransition{}, false, err
		}
		result = transition
		result.membership.Status = membershipLeaving
		result.changed = true
		if !result.membership.IsMember {
			return result, false, nil
		}
	}
	terminal, err := reconcileRemovalMembership(ctx, api, row, result.membership, checkedAt)
	return result, terminal, err
}

func checkPublicMembership(ctx context.Context, api *tg.Client, target string) (tg.InputPeerClass, string, bool, error) {
	peer, title, err := resolveTelegramPeer(ctx, api, target, false)
	if err != nil {
		return nil, "", false, err
	}
	channel, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return nil, "", false, nil
	}
	_, err = api.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{
		Channel:     &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
		Participant: &tg.InputPeerSelf{},
	})
	if err == nil {
		return peer, title, true, nil
	}
	if tgerr.Is(err, "USER_NOT_PARTICIPANT", "CHANNEL_PRIVATE") {
		return peer, title, false, nil
	}
	return nil, "", false, err
}

func reconcileCatalogMembershipIntents(ctx context.Context, api *tg.Client, catalogs domain.CatalogRepository, memberships accountMembershipCatalog, accountID domain.ID, catalog domain.SourceCatalog, rows []domain.Channel, assigned map[domain.ID]struct{}, checkedAt time.Time) ([]domain.Channel, map[domain.ID]tg.InputPeerClass, error) {
	resolvedPeers := make(map[domain.ID]tg.InputPeerClass)
	removals, supportsRemoval := catalogs.(catalogRemovalCoordinator)
	for index, row := range rows {
		if row.RemovalRequestedAt != nil {
			if memberships == nil || !supportsRemoval {
				continue
			}
			membership, found, err := memberships.LoadMembership(ctx, accountID, catalog, row.ID)
			if err != nil {
				return nil, nil, err
			}
			if !found {
				if err := removals.CompleteCatalogRemoval(ctx, catalog, row.ID); err != nil {
					return nil, nil, err
				}
				continue
			}
			if membership.Status == membershipPendingApproval {
				transition, err := reconcileMembershipIntent(ctx, api, row, membership, checkedAt)
				if err != nil {
					membership.LastCheckAt = timePointer(checkedAt)
					membership.LastError = err.Error()
					if saveErr := memberships.SaveMembership(ctx, catalog, membership); saveErr != nil {
						return nil, nil, saveErr
					}
					if isTerminalInviteHashError(err) {
						continue
					}
					return nil, nil, accountRPCError(accountID, err)
				}
				membership = transition.membership
				if membership.Status == membershipPendingApproval {
					if transition.changed {
						if err := memberships.SaveMembership(ctx, catalog, membership); err != nil {
							return nil, nil, err
						}
					}
					continue
				}
				peer, title, err := resolveExplicitDiscussionTarget(ctx, api, row.Link, transition.peer, transition.title)
				if err != nil {
					return nil, nil, accountRPCError(accountID, err)
				}
				row.TelegramID = inputPeerID(peer)
				if title != "" {
					row.Title = title
				}
				row.UpdatedAt = checkedAt.UTC()
				if err := catalogs.Save(ctx, catalog, row); err != nil {
					return nil, nil, err
				}
				rows[index] = row
				if transition.changed {
					if err := memberships.SaveMembership(ctx, catalog, membership); err != nil {
						return nil, nil, err
					}
				}
			}
			terminal, err := reconcileRemovalMembership(ctx, api, row, membership, checkedAt)
			if err != nil {
				membership.LastCheckAt = timePointer(checkedAt)
				membership.LastError = err.Error()
				if membership.Status != membershipPendingApproval {
					membership.Status = "error"
				}
				if saveErr := memberships.SaveMembership(ctx, catalog, membership); saveErr != nil {
					return nil, nil, saveErr
				}
				return nil, nil, accountRPCError(accountID, err)
			}
			if terminal {
				if err := removals.DeleteMembership(ctx, catalog, accountID, row.ID); err != nil {
					return nil, nil, err
				}
				if err := removals.CompleteCatalogRemoval(ctx, catalog, row.ID); err != nil {
					return nil, nil, err
				}
			}
			continue
		}
		if memberships != nil {
			membership, found, err := memberships.LoadMembership(ctx, accountID, catalog, row.ID)
			if err != nil {
				return nil, nil, err
			}
			if found && membership.Status == membershipLeaving {
				deletions, ok := memberships.(membershipIntentDeletionStore)
				if !ok {
					return nil, nil, errors.New("membership store does not support deletion")
				}
				transition, terminal, err := reconcileLeavingMembership(ctx, api, row, membership, checkedAt)
				if err != nil {
					membership.LastCheckAt = timePointer(checkedAt)
					membership.LastError = err.Error()
					if saveErr := memberships.SaveMembership(ctx, catalog, membership); saveErr != nil {
						return nil, nil, saveErr
					}
					return nil, nil, accountRPCError(accountID, err)
				}
				if terminal {
					if err := deletions.DeleteMembership(ctx, catalog, accountID, row.ID); err != nil {
						return nil, nil, err
					}
					continue
				}
				if transition.changed {
					if err := memberships.SaveMembership(ctx, catalog, transition.membership); err != nil {
						return nil, nil, err
					}
				}
				continue
			}
		}
		if isDerivedScoutDiscussion(row) {
			continue
		}
		if row.Active {
			if _, ok := assigned[row.ID]; !ok {
				continue
			}
		}
		membership, found, err := loadAccountMembershipIntent(ctx, memberships, accountID, catalog, row)
		if err != nil {
			return nil, nil, err
		}
		if !found || (membership.Status != membershipJoining && membership.Status != membershipPendingApproval) {
			continue
		}
		transition, err := reconcileMembershipIntent(ctx, api, row, membership, checkedAt)
		if err != nil {
			return nil, nil, accountRPCError(accountID, err)
		}
		if transition.changed && memberships != nil {
			if err := memberships.SaveMembership(ctx, catalog, transition.membership); err != nil {
				return nil, nil, err
			}
		}
		if !transition.membership.IsMember || transition.membership.Status != membershipMember || transition.peer == nil {
			continue
		}
		peer, title, err := resolveExplicitDiscussionTarget(ctx, api, row.Link, transition.peer, transition.title)
		if err != nil {
			return nil, nil, accountRPCError(accountID, err)
		}
		row.TelegramID = inputPeerID(peer)
		if title != "" {
			row.Title = title
		}
		row.Status = domain.ChannelReady
		row.UpdatedAt = checkedAt.UTC()
		if err := catalogs.Save(ctx, catalog, row); err != nil {
			return nil, nil, err
		}
		rows[index] = row
		resolvedPeers[row.ID] = peer
	}
	return rows, resolvedPeers, nil
}

func (r *membershipTransition) markMember(checkedAt time.Time, outcome membershipJoinOutcome) {
	r.membership.IsMember = true
	r.membership.Status = membershipMember
	r.membership.LastCheckAt = timePointer(checkedAt)
	if outcome == membershipNewlyJoined && r.membership.JoinedAt == nil {
		r.membership.JoinedAt = timePointer(checkedAt)
		if domain.ValidateGroupRestHours(r.membership.RestDurationHours) == nil {
			r.membership.RestStartedAt = timePointer(checkedAt)
			r.membership.RestUntil = timePointer(checkedAt.Add(time.Duration(r.membership.RestDurationHours) * time.Hour))
		}
	}
	r.membership.LastError = ""
	r.changed = true
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func pendingApprovalMembership(accountID, channelID domain.ID, checkedAt time.Time) domain.ChannelMembership {
	return domain.ChannelMembership{
		AccountID:          accountID,
		ChannelID:          channelID,
		Status:             membershipPendingApproval,
		RequestSubmittedAt: timePointer(checkedAt),
		LastCheckAt:        timePointer(checkedAt),
	}
}

func isInviteRequestSent(err error) bool {
	return tg.IsInviteRequestSent(err) || tgerr.Is(err, tg.ErrInviteRequestSent)
}

func isTerminalInviteHashError(err error) bool {
	return tgerr.Is(err, "INVITE_HASH_INVALID", "INVITE_HASH_EXPIRED")
}
