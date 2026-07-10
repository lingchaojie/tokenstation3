.PHONY: build build-backend build-frontend build-datamanagementd check-generate test test-backend test-frontend test-frontend-critical test-datamanagementd secret-scan

FRONTEND_CRITICAL_VITEST := \
	src/views/auth/__tests__/LinuxDoCallbackView.spec.ts \
	src/views/auth/__tests__/WechatCallbackView.spec.ts \
	src/views/user/__tests__/PaymentView.spec.ts \
	src/views/user/SubscriptionsView.spec.ts \
	src/views/user/__tests__/PaymentResultView.spec.ts \
	src/components/payment/__tests__/SubscriptionPlanCard.spec.ts \
	src/components/user/dashboard/UserDashboardStats.spec.ts \
	src/components/user/profile/__tests__/ProfileInfoCard.spec.ts \
	src/views/admin/__tests__/SettingsView.spec.ts \
	src/views/admin/__tests__/UsersView.spec.ts \
	src/views/admin/orders/__tests__/AdminPaymentPlansView.spec.ts \
	src/views/admin/orders/__tests__/PlanEditDialog.spec.ts \
	src/router/__tests__/admin-my-account-dashboard-route.spec.ts \
	src/utils/__tests__/analytics51la.spec.ts

# 一键编译前后端
build: build-backend build-frontend

# 编译后端（复用 backend/Makefile）
build-backend:
	@$(MAKE) -C backend build

# 编译前端（需要已安装依赖）
build-frontend:
	@pnpm --dir frontend run build

# 校验生成代码与提交内容一致，防止 ent/wire 漂移进入 CI
check-generate:
	@$(MAKE) -C backend check-generate

# 编译 datamanagementd（宿主机数据管理进程）
build-datamanagementd:
	@cd datamanagement && go build -o datamanagementd ./cmd/datamanagementd

# 运行测试（后端 + 前端）
test: test-backend test-frontend

test-backend:
	@$(MAKE) -C backend test

test-frontend:
	@pnpm --dir frontend run lint:check
	@pnpm --dir frontend run typecheck
	@$(MAKE) test-frontend-critical

test-frontend-critical:
	@pnpm --dir frontend exec vitest run $(FRONTEND_CRITICAL_VITEST)
