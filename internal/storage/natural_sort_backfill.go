package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/RecRivenVI/gallery/internal/querytext"
)

const naturalSortKeyEncodingVersion = 2

// ensureControlCreatorNaturalSortKeyEncoding 在 control.db 对外可用前补齐 CanonicalCreator
// 的可重建排序键。它不改变 Creator ID、名称、合并或 Binding 等用户事实；事务提交前
// 版本标记保持旧值，进程中断后会在下一次启动整体重试。
func ensureControlCreatorNaturalSortKeyEncoding(ctx context.Context, db *sql.DB) error {
	var rawVersion string
	if err := db.QueryRowContext(ctx,
		"SELECT value FROM gallery_control_meta WHERE key='creator_natural_sort_key_encoding'").Scan(&rawVersion); err != nil {
		return err
	}
	version, err := strconv.Atoi(rawVersion)
	if err != nil || version < 1 || version > naturalSortKeyEncodingVersion {
		return fmt.Errorf("creator natural sort key encoding version 无效: %q", rawVersion)
	}
	if version == naturalSortKeyEncodingVersion {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := backfillNaturalSortTable(ctx, tx, "canonical_creators", "name", "sort_name_key"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE gallery_control_meta SET value=? WHERE key='creator_natural_sort_key_encoding'",
		strconv.Itoa(naturalSortKeyEncodingVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// ensureNaturalSortKeyEncoding 在 Catalog 对外可用前同步升级持久排序键。排序键完全由同一行的
// title/name 重建，不读取 Source，也不改变任何不可重建事实。使用 rowid 小批读取，避免把大型
// Catalog 的全部标题一次装入内存；整个升级处于一个 IMMEDIATE 事务中，版本标记只在全部行成功
// 后推进，进程中断会整体回滚并在下次启动重试。
func ensureNaturalSortKeyEncoding(ctx context.Context, db *sql.DB) error {
	var rawVersion string
	if err := db.QueryRowContext(ctx,
		"SELECT value FROM gallery_catalog_meta WHERE key='natural_sort_key_encoding'").Scan(&rawVersion); err != nil {
		return err
	}
	version, err := strconv.Atoi(rawVersion)
	if err != nil || version < 1 || version > naturalSortKeyEncodingVersion {
		return fmt.Errorf("natural sort key encoding version 无效: %q", rawVersion)
	}
	if version == naturalSortKeyEncodingVersion {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 排序键回填会改写 WorkProjection 与搜索窄候选；若升级前恰有崩溃遗留的已验证
	// staging candidate，其旧封印必须与回填在同一事务内失效，不能让后续恢复路径把
	// v1/v2 混合候选直接发布。已发布 revision 的历史封印只是审计事实，删除也无害。
	if _, err := tx.ExecContext(ctx, "DELETE FROM candidate_validation_seals"); err != nil {
		return err
	}
	for _, table := range []struct {
		name, source, target string
	}{
		{name: "work_projections", source: "title", target: "sort_title_key"},
		{name: "creator_projections", source: "name", target: "sort_name_key"},
	} {
		if err := backfillNaturalSortTable(ctx, tx, table.name, table.source, table.target); err != nil {
			return err
		}
	}
	// catalog v18 把分页前使用的 sort_title_key 同步物化到搜索窄候选表。迁移顺序保证
	// 该表在本函数运行前存在；必须与 WorkProjection 在同一事务内更新，不能让版本标记
	// 已推进而候选仍保留 v1 编码，否则同一 publication 的 browse/search 会产生不同顺序。
	if _, err := tx.ExecContext(ctx, `UPDATE work_search_candidates AS c
SET sort_title_key = (
  SELECT w.sort_title_key FROM work_projections AS w
  WHERE w.catalog_revision_id=c.catalog_revision_id
    AND w.overlay_revision_id=c.overlay_revision_id
    AND w.work_id=c.work_id
)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE gallery_catalog_meta SET value=? WHERE key='natural_sort_key_encoding'",
		strconv.Itoa(naturalSortKeyEncodingVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func backfillNaturalSortTable(ctx context.Context, tx *sql.Tx, table, sourceColumn, targetColumn string) error {
	// 标识符只来自上方封闭常量表，不能接收外部输入。
	selectSQL := fmt.Sprintf("SELECT rowid, %s FROM %s WHERE rowid>? ORDER BY rowid LIMIT 1000", sourceColumn, table)
	updateSQL := fmt.Sprintf("UPDATE %s SET %s=? WHERE rowid=?", table, targetColumn)
	update, err := tx.PrepareContext(ctx, updateSQL)
	if err != nil {
		return err
	}
	defer update.Close()

	var after int64
	for {
		rows, err := tx.QueryContext(ctx, selectSQL, after)
		if err != nil {
			return err
		}
		type entry struct {
			rowID int64
			value string
		}
		batch := make([]entry, 0, 1000)
		for rows.Next() {
			var item entry
			if err := rows.Scan(&item.rowID, &item.value); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, item := range batch {
			if _, err := update.ExecContext(ctx, querytext.NaturalSortKey(item.value), item.rowID); err != nil {
				return err
			}
		}
		after = batch[len(batch)-1].rowID
	}
}
