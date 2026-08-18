-- 0008_sync_mode.sql
-- app_config 增加 sync_mode（同步方式）：auto（自动同步，默认）/ manual（手动同步）。
-- 手动模式：不启动后台 SSE/心跳/轮询，也不在保存条目后自动推送，仅手动点「立即同步」时拉取+推送。
ALTER TABLE app_config ADD COLUMN sync_mode TEXT NOT NULL DEFAULT 'auto';
