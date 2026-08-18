-- 0007_group_state_name.sql
-- group_state 增加 name（组名）：新成员入组同步后本地能展示组名，
-- 并让 EditView 新建条目的组回退链拿到组 id/name（修「缺少组 ID」）。
ALTER TABLE group_state ADD COLUMN name TEXT;
