// Penjaga independen DevControl — dipasang MANUAL di akun Cloudflare Anda
// (bukan oleh DevControl), sehingga tetap berjaga walaupun DevControl diambil alih.
//
// Cloudflare → Workers & Pages → Create → Hello World → tempel file ini → Deploy.
// Settings → Variables and Secrets (tipe Secret):
//   DEVCONTROL_URL      https://alamat-devcontrol-anda (tanpa / di akhir)
//   HEARTBEAT_SECRET    sama persis dengan DEVCONTROL_HEARTBEAT_SECRET di Vercel
//   TELEGRAM_BOT_TOKEN  token bot dari @BotFather
//   TELEGRAM_CHAT_ID    chat ID Anda
// Opsional: binding KV bernama WATCHDOG_KV agar peringatan yang sama tidak dikirim berulang.
// Settings → Triggers → Cron Triggers: */15 * * * *

async function hmacHex(secret, message) {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const signature = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(message));
  return [...new Uint8Array(signature)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

async function telegram(env, text) {
  await fetch(`https://api.telegram.org/bot${env.TELEGRAM_BOT_TOKEN}/sendMessage`, {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ chat_id: env.TELEGRAM_CHAT_ID, text, disable_web_page_preview: true }),
  });
}

// Sends each distinct problem once (with KV) or at most once an hour (without KV).
async function alarm(env, key, text) {
  if (env.WATCHDOG_KV) {
    if (await env.WATCHDOG_KV.get(key)) return;
    await env.WATCHDOG_KV.put(key, "1", { expirationTtl: 6 * 3600 });
  } else if (new Date().getUTCMinutes() >= 15) {
    return;
  }
  await telegram(env, `🛡️ Penjaga DevControl\n${text}`);
}

async function check(env) {
  const nonce = crypto.randomUUID();
  let response;
  try {
    response = await fetch(`${env.DEVCONTROL_URL}/api/heartbeat?nonce=${nonce}`, { headers: { Authorization: `Bearer ${env.HEARTBEAT_SECRET}` } });
  } catch {
    return alarm(env, "down", "DevControl tidak dapat dihubungi. Periksa apakah aplikasi dimatikan atau domainnya diubah.");
  }
  if (!response.ok) return alarm(env, `http-${response.status}`, `DevControl menjawab HTTP ${response.status} pada heartbeat. Kalau rahasia heartbeat tidak Anda ubah, anggap aplikasinya sudah diganti.`);
  const body = await response.json().catch(() => null);
  if (!body) return alarm(env, "bad-json", "Jawaban heartbeat DevControl tidak terbaca.");
  const expected = await hmacHex(env.HEARTBEAT_SECRET, `${nonce}|${body.time}|${body.open_alerts}|${body.lockdown}|${body.last_patrol}`);
  if (expected !== body.signature) return alarm(env, "signature", "Tanda tangan heartbeat SALAH. Kemungkinan DevControl sudah diganti atau rahasianya diubah orang lain.");
  if (Math.abs(Date.now() - Date.parse(body.time)) > 5 * 60 * 1000) return alarm(env, "clock", "Waktu pada heartbeat DevControl tidak cocok (jawaban lama diputar ulang?).");
  if (body.open_alerts > 0) await alarm(env, `open-${body.open_alerts}`, `${body.open_alerts} peringatan keamanan Siaga/Darurat belum dijawab di Pusat Keamanan.`);
  if (body.lockdown) await alarm(env, "lockdown", "Mode Darurat DevControl sedang aktif.");
  const patrol = Date.parse(`${String(body.last_patrol).replace(" ", "T")}Z`);
  if (!body.last_patrol || Number.isNaN(patrol) || Date.now() - patrol > 60 * 60 * 1000) await alarm(env, "patrol", "Patroli keamanan DevControl tidak berjalan lebih dari 1 jam.");
  if (env.WATCHDOG_KV && body.commit) {
    const previous = await env.WATCHDOG_KV.get("commit");
    if (previous && previous !== body.commit) await telegram(env, `🛡️ Penjaga DevControl\nVersi DevControl berganti: ${previous.slice(0, 12)} → ${body.commit.slice(0, 12)}. Kalau bukan Anda yang memperbarui, periksa repo sekarang.`);
    await env.WATCHDOG_KV.put("commit", body.commit);
  }
}

export default {
  async scheduled(_event, env, ctx) { ctx.waitUntil(check(env)); },
  async fetch() { return new Response("Penjaga DevControl aktif.", { status: 200 }); },
};
