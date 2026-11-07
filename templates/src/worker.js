// One request touches every binding, so a test can see D1, KV, R2 and the secret all working. It deletes the R2
// object it wrote, so nothing is left in a bucket and mise run cloudflare:delete deletes the buckets too.
export default {
  async fetch(_request, env) {
    const stamp = new Date().toISOString();
    await env.DB.exec("CREATE TABLE IF NOT EXISTS hits (at TEXT)");
    await env.DB.prepare("INSERT INTO hits (at) VALUES (?)").bind(stamp).run();
    const d1 = await env.DB.prepare("SELECT at FROM hits WHERE at = ?").bind(stamp).first("at");
    await env.KV.put("stamp", stamp);
    const kv = await env.KV.get("stamp");
    await env.BUCKET.put("stamp.txt", stamp);
    const r2 = await (await env.BUCKET.get("stamp.txt"))?.text();
    await env.BUCKET.delete("stamp.txt");
    return Response.json({ d1: d1 === stamp, kv: kv === stamp, r2: r2 === stamp, secret: Boolean(env.GREETING) });
  },
};
