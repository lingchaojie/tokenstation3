package service

// kiro 配置 resolver：组优先、账号兜底。
// - 原生 kiro 组（isKiroGroup）：取 group 配置，行为与改动前完全一致。
// - kiro direct 账号混入非 kiro 组：取账号自身 Extra 配置。
// - 其它（如 anthropic 账号）：返回安全默认（短路）。

func resolveKiroEndpointMode(account *Account, group *Group) string {
	if isKiroGroup(group) {
		return group.EffectiveKiroEndpointMode()
	}
	if isKiroDirectModeAccount(account) {
		return account.KiroEndpointMode()
	}
	return KiroEndpointModeQ
}

func resolveKiroCacheEmulation(account *Account, group *Group) (enabled bool, ratio float64) {
	if isKiroGroup(group) {
		return group.EffectiveKiroCacheEmulationEnabled(), group.EffectiveKiroCacheEmulationRatio()
	}
	if isKiroDirectModeAccount(account) {
		return account.KiroCacheEmulationEnabled(), account.KiroCacheEmulationRatio()
	}
	return false, 0
}

func resolveKiroStickySessionTTLSeconds(account *Account, group *Group) int {
	if isKiroGroup(group) {
		return group.EffectiveKiroStickySessionTTLSeconds()
	}
	if isKiroDirectModeAccount(account) {
		return account.KiroStickySessionTTLSeconds()
	}
	return 0
}
