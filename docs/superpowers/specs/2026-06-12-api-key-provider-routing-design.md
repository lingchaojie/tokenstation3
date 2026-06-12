# API Key Provider Routing Design

## Summary

Normal users should not see or choose account groups when creating API keys. They choose only a fixed key type: Anthropic or OpenAI. The backend resolves that type to an admin-managed group at creation time, stores both the fixed key type and the resolved group on the API key, and leaves existing gateway routing, billing, and account selection behavior based on `apiKey.GroupID` intact.

Admins still manage groups, account-to-group membership, global default groups, and per-user default routing groups.

## Goals

- Hide group selection from normal users.
- Let normal users create either Anthropic or OpenAI keys.
- Keep key type fixed after creation for normal users.
- Resolve normal-user key creation to admin-managed groups.
- Support global default Anthropic/OpenAI groups.
- Support per-user Anthropic/OpenAI default group overrides.
- Preserve existing gateway behavior that routes by the API key's group platform.
- Preserve current compatibility behavior:
  - Anthropic keys can still use existing OpenAI-compatible Anthropic gateway endpoints such as `/v1/chat/completions` and `/v1/responses`.
  - OpenAI keys can use `/v1/messages` only when their OpenAI group allows messages dispatch.
- Preserve historical API keys without silently changing their group routing.

## Non-goals

- Do not implement request-time dynamic effective-group routing.
- Do not make admin changes to user defaults automatically rewrite existing API keys.
- Do not remove admin group CRUD or account-group management.
- Do not create ungrouped API keys for normal-user provider creation.
- Do not add bulk rebinding for existing keys in this iteration.

## Architecture

Use fixed key type plus creation-time group resolution.

A normal user creates a key with `key_type` set to `anthropic` or `openai`. The backend resolves the actual group in this order:

1. Per-user default group for `(user_id, key_type)`.
2. Global default group for `key_type`.

The resolved group is validated and stored in `api_keys.group_id`. The selected key type is stored in a new API-key field. Gateway request handling continues to use `apiKey.GroupID` and `apiKey.Group.Platform`, minimizing changes to the hot path.

## Data Model

### API keys

Add an API-key type field with allowed values:

- `anthropic`
- `openai`

Historical keys may not have this field populated. API responses should derive a display type as follows:

1. If the stored key type is present, use it.
2. Else if the key has a group, use the group platform when it is Anthropic or OpenAI.
3. Else return an unknown or unconfigured state for display.

### Settings

Add global default group settings:

- `default_anthropic_group_id`
- `default_openai_group_id`

Admin settings must only allow active groups whose platform matches the setting.

### Per-user routing overrides

Add a per-user routing configuration for default key creation:

- `user_id`
- `key_type`
- `group_id`

This configuration controls future normal-user key creation only. If no per-user override exists, creation falls back to the global default group.

## User Experience

### Normal user API key page

- Remove the group selector from the create/edit API key modal.
- Add a key type selector with two options: Anthropic and OpenAI.
- Creation sends `key_type` and does not send `group_id`.
- Editing an existing key does not allow normal users to change group or key type.
- Lists should show key type instead of the real group name for normal users. Existing untyped keys use derived display type.

### Admin experience

- Existing group CRUD remains available.
- Admins can still create groups, bind accounts to groups, and manage group-specific policies.
- Admin settings can choose global default Anthropic/OpenAI groups.
- Admin user management can configure per-user Anthropic/OpenAI default groups.
- Per-user default changes affect future key creation only.

## Backend Flow

### Normal-user key creation

1. Validate `key_type` is `anthropic` or `openai`.
2. Reject normal-user attempts to provide `group_id`; normal users must use admin-managed routing.
3. Resolve group using per-user override, then global default.
4. Validate the resolved group:
   - exists
   - active
   - not deleted
   - platform matches `key_type`
   - user can bind the group by the existing permission rules
5. Create the API key with both `key_type` and resolved `group_id`.

Existing permission rules remain authoritative:

- Standard public groups are bindable.
- Standard exclusive groups require user allowed-group permission.
- Subscription groups require an active subscription for that user and group.

### Admin-created or admin-managed keys

Admin flows may continue to specify explicit groups. If an admin sets both key type and group, the backend should validate that the group platform matches the key type. If an admin changes a key's group, the key type should remain consistent with the new group platform or be updated through explicit admin behavior.

### Request routing

No broad gateway rewrite is required.

- Existing gateway routes continue to route by `apiKey.Group.Platform`.
- OpenAI keys use OpenAI groups and OpenAI gateway paths.
- Anthropic keys use Anthropic groups and Anthropic gateway paths.
- OpenAI keys calling `/v1/messages` continue to depend on `allow_messages_dispatch` on the OpenAI group.
- Anthropic keys retain existing compatibility for OpenAI-shaped endpoints handled by the Anthropic-compatible gateway.

## Error Handling

### Missing default group

If no per-user or global default group exists for the requested key type, normal-user creation fails with a clear message telling the user the administrator has not configured a default group.

### Platform mismatch

If a default or per-user route points an OpenAI key to a non-OpenAI group, or an Anthropic key to a non-Anthropic group, saving the admin configuration should fail. API key creation must also validate this and fail as a backend safety net.

### Deleted or disabled group

Creating a new key fails if the resolved group is deleted or inactive. Existing keys keep current runtime behavior and are blocked by the existing group availability middleware when used.

### Subscription group without subscription

If the resolved group is subscription-type and the user has no active subscription for it, creation fails. The system does not automatically fall back to a balance group.

### Exclusive group without permission

If the resolved group is an exclusive standard group and the user lacks allowed-group permission, creation fails. Admin configuration should also prevent saving this invalid user route where practical.

### Historical ungrouped key

Historical ungrouped keys are not auto-migrated. They display as unknown or unconfigured and continue to follow the existing ungrouped-key runtime behavior.

## Testing Plan

### Backend service tests

- Normal user creates Anthropic key using per-user override.
- Normal user creates Anthropic key falling back to global default.
- Normal user creates OpenAI key using per-user override.
- Normal user creates OpenAI key falling back to global default.
- Creation fails when the default group is missing.
- Creation fails when the default group is inactive or deleted.
- Creation fails when key type and group platform mismatch.
- Creation fails for subscription group without active subscription.
- Creation fails for exclusive group without allowed-group permission.
- Historical key display type is derived from group platform when stored key type is absent.
- Historical key without group returns unknown or unconfigured display type.
- Admin user-route save validates platform and permission constraints.

### Backend handler and contract tests

- `POST /keys` accepts `key_type` for normal users.
- `POST /keys` does not require `group_id` for normal users.
- Normal-user `POST /keys` rejects explicit `group_id`.
- Settings endpoints read and write default Anthropic/OpenAI group IDs.
- Admin user-management endpoints read and write per-user routing defaults.

### Frontend tests

- User key create modal does not render group selector.
- User key create modal renders Anthropic/OpenAI type selector.
- Create payload includes `key_type` and excludes `group_id`.
- Edit modal does not allow normal users to change key type or group.
- Admin settings page can render and submit default Anthropic/OpenAI groups.
- Admin users page can render and submit per-user Anthropic/OpenAI default groups.

### Manual verification

- Run relevant Go tests.
- Run relevant frontend tests and typecheck.
- Start the frontend and verify:
  - normal user create-key modal only exposes provider type
  - key creation succeeds when defaults are configured
  - key creation fails clearly when defaults are missing
  - admin can configure global defaults and per-user overrides

## Rollout Notes

Existing API keys are preserved. New normal-user key creation requires provider type and a configured default group. Operators should configure global default Anthropic/OpenAI groups before enabling the new user experience in production.
