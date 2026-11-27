// A D1-shaped wrapper over node:sqlite, so tests run real SQL with no
// Cloudflare credentials and no npm install.
import { DatabaseSync } from 'node:sqlite';

class Statement {
  constructor(db, sql, params = []) {
    this.db = db;
    this.sql = sql;
    this.params = params;
  }
  bind(...params) {
    return new Statement(this.db, this.sql, params);
  }
  async run() {
    const r = this.db.prepare(this.sql).run(...this.params);
    return { success: true, meta: { changes: Number(r.changes) } };
  }
  async all() {
    return { success: true, results: this.db.prepare(this.sql).all(...this.params).map((r) => ({ ...r })) };
  }
  async first() {
    const r = this.db.prepare(this.sql).get(...this.params);
    return r ? { ...r } : null;
  }
}

export class D1Fake {
  constructor(migrationSql) {
    this.db = new DatabaseSync(':memory:');
    if (migrationSql) this.db.exec(migrationSql);
  }
  prepare(sql) {
    return new Statement(this.db, sql);
  }
  // One transaction: all statements apply or none do.
  async batch(statements) {
    this.db.exec('BEGIN');
    try {
      const out = [];
      for (const s of statements) out.push(await s.run());
      this.db.exec('COMMIT');
      return out;
    } catch (err) {
      this.db.exec('ROLLBACK');
      throw err;
    }
  }
}
