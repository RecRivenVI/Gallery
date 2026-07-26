-- 新增 `files.browse` capability 的角色映射。
--
-- 文件根浏览需要独立 capability，**不能复用 `library.read`**。后者的 scope 在实践中总是
-- `{Kind:"library"|"source"}`，而文件根既不是 Library 也不是 Source，请求只能以 global scope 发出；
-- 而 `scopeMatches` 对 global 请求不匹配任何非 global grant，因此「禁止此人读某 Library」的 deny
-- grant 完全不会约束文件根浏览——而文件根恰恰是那些 Library 底层文件的父目录。这会原样重演
-- EV-39 登记的 `AUTHZ-1`，且危害更大。
--
-- 历史 migration 内容受 SHA-256 锁定且不可修改，因此角色映射只能由本次新增 migration 追加。
--
-- 只授予 owner：文件根浏览可以看到尚未被任何 Source 覆盖的目录，超出普通 operator 的浏览范围。
-- viewer 与 operator 需要该能力时由显式 Grant 授予，而不是默认放开。
INSERT INTO security_role_capabilities (role_id, capability)
VALUES ('owner', 'files.browse');
