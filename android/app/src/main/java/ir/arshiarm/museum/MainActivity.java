package ir.arshiarm.museum;

import android.app.Activity;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageInfo;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.view.WindowManager;
import android.webkit.JavascriptInterface;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;

import androidx.webkit.WebViewAssetLoader;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.Iterator;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.regex.Pattern;

/**
 * Shows the museum full screen. The museum files ship inside the app, and newer files
 * are fetched from the museum's online repository, so a new APK is rarely needed.
 * It also brings announcements to everyone who has the app. Created By ArshiaRM
 */
public class MainActivity extends Activity {

    /** Where the online copy of the museum lives. The online manifest can add more mirrors. */
    private static final String[] DEFAULT_BASES = {
            "https://raw.githubusercontent.com/arshiarm89-dotcom/museum/main/",
            "https://cdn.jsdelivr.net/gh/arshiarm89-dotcom/museum@main/",
            "https://arshiarm89-dotcom.github.io/museum/",
            "https://github.com/arshiarm89-dotcom/museum/raw/main/"
    };
    /** Updates smaller than this download on their own; bigger ones ask first (mobile data). */
    private static final long ASK_ABOVE_BYTES = 3_000_000L;
    private static final long RECHECK_MS = 30L * 60L * 1000L;
    private static final Pattern SAFE_PATH = Pattern.compile("^[A-Za-z0-9_\\-][A-Za-z0-9_.\\-]*(/[A-Za-z0-9_\\-][A-Za-z0-9_.\\-]*)*$");
    private static final String START_URL = "https://appassets.androidplatform.net/assets/index.html";

    private WebView web;
    private File liveDir;
    private SharedPreferences prefs;
    private final ExecutorService bg = Executors.newSingleThreadExecutor();
    private final Handler ui = new Handler(Looper.getMainLooper());

    private volatile long lastCheck = 0;
    private volatile boolean busy = false;
    private volatile JSONObject waitingManifest = null;
    private volatile List<String> waitingFiles = null;
    private volatile long waitingBytes = 0;
    private volatile String workingBase = null;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        prefs = getSharedPreferences("museum", MODE_PRIVATE);
        liveDir = new File(getFilesDir(), "live");
        dropLiveIfOlderThanApp();

        final WebViewAssetLoader.AssetsPathHandler assets = new WebViewAssetLoader.AssetsPathHandler(this);
        final WebViewAssetLoader loader = new WebViewAssetLoader.Builder()
                .addPathHandler("/assets/", path -> {
                    WebResourceResponse r = fromLive(path);
                    return r != null ? r : assets.handle(path);
                })
                .build();

        web = new WebView(this);
        web.setBackgroundColor(0xFF14120E);
        WebSettings settings = web.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setMediaPlaybackRequiresUserGesture(false);
        settings.setAllowFileAccess(false);
        settings.setAllowContentAccess(false);
        // The online visit server (on the museum's own VPS) is reached with ws://, so allow it from this page.
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);
        web.addJavascriptInterface(new Bridge(), "MuseumApp");

        web.setWebChromeClient(new WebChromeClient());
        web.setWebViewClient(new WebViewClient() {
            @Override
            public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
                return loader.shouldInterceptRequest(request.getUrl());
            }

            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                Uri u = request.getUrl();
                if ("appassets.androidplatform.net".equals(u.getHost())) return false;
                openOutside(u.toString());
                return true;
            }

            @Override
            public void onPageFinished(WebView view, String url) {
                if (System.currentTimeMillis() - lastCheck > 60_000L) checkOnline();
            }
        });

        setContentView(web);
        hideBars();
        web.loadUrl(START_URL);
    }

    /* ---------------- serving the downloaded (newer) files ---------------- */

    private WebResourceResponse fromLive(String path) {
        try {
            if (path == null || !SAFE_PATH.matcher(path).matches() || path.equals("manifest.json")) return null;
            File f = new File(liveDir, path);
            if (!f.isFile() || !f.getCanonicalPath().startsWith(liveDir.getCanonicalPath() + File.separator)) return null;
            String mime = mimeOf(path);
            WebResourceResponse r = new WebResourceResponse(mime, mime.startsWith("text/") || mime.endsWith("javascript") || mime.endsWith("json") ? "utf-8" : null, new FileInputStream(f));
            return r;
        } catch (IOException e) {
            return null;
        }
    }

    private static String mimeOf(String path) {
        String p = path.toLowerCase(Locale.ROOT);
        if (p.endsWith(".html") || p.endsWith(".htm")) return "text/html";
        if (p.endsWith(".js")) return "text/javascript";
        if (p.endsWith(".css")) return "text/css";
        if (p.endsWith(".json")) return "application/json";
        if (p.endsWith(".webp")) return "image/webp";
        if (p.endsWith(".png")) return "image/png";
        if (p.endsWith(".jpg") || p.endsWith(".jpeg")) return "image/jpeg";
        if (p.endsWith(".gif")) return "image/gif";
        if (p.endsWith(".svg")) return "image/svg+xml";
        if (p.endsWith(".mp3")) return "audio/mpeg";
        if (p.endsWith(".ogg")) return "audio/ogg";
        if (p.endsWith(".wav")) return "audio/wav";
        if (p.endsWith(".m4a")) return "audio/mp4";
        if (p.endsWith(".mp4")) return "video/mp4";
        if (p.endsWith(".webm")) return "video/webm";
        if (p.endsWith(".woff2")) return "font/woff2";
        if (p.endsWith(".woff")) return "font/woff";
        if (p.endsWith(".ttf")) return "font/ttf";
        return "application/octet-stream";
    }

    /* ---------------- manifests ---------------- */

    private JSONObject bundledManifest() {
        try (InputStream in = getAssets().open("manifest.json")) {
            return new JSONObject(new String(readAll(in), StandardCharsets.UTF_8));
        } catch (Exception e) {
            return emptyManifest();
        }
    }

    private JSONObject liveManifest() {
        File f = new File(liveDir, "manifest.json");
        if (!f.isFile()) return null;
        try (InputStream in = new FileInputStream(f)) {
            return new JSONObject(new String(readAll(in), StandardCharsets.UTF_8));
        } catch (Exception e) {
            return null;
        }
    }

    private JSONObject currentManifest() {
        JSONObject m = liveManifest();
        return m != null ? m : bundledManifest();
    }

    private static JSONObject emptyManifest() {
        try {
            return new JSONObject().put("version", 0).put("files", new JSONObject());
        } catch (Exception e) {
            return new JSONObject();
        }
    }

    /** After a newer APK is installed, its own files are newest, so older downloads are dropped. */
    private void dropLiveIfOlderThanApp() {
        JSONObject live = liveManifest();
        if (live != null && live.optLong("version") <= bundledManifest().optLong("version")) deleteTree(liveDir);
    }

    /* ---------------- checking online ---------------- */

    private List<String> bases() {
        LinkedHashSet<String> set = new LinkedHashSet<>();
        String pref = prefs.getString("base", null);
        if (pref != null) set.add(pref);
        try {
            JSONArray extra = new JSONArray(prefs.getString("mirrors", "[]"));
            for (int i = 0; i < extra.length(); i++) {
                String b = extra.optString(i, "");
                if (b.startsWith("https://")) set.add(b.endsWith("/") ? b : b + "/");
            }
        } catch (Exception ignored) { }
        for (String b : DEFAULT_BASES) set.add(b);
        return new ArrayList<>(set);
    }

    private void checkOnline() {
        if (busy) return;
        lastCheck = System.currentTimeMillis();
        bg.execute(() -> {
            JSONObject remote = null;
            for (String b : bases()) {
                try {
                    remote = new JSONObject(new String(get(b + "manifest.json?t=" + System.currentTimeMillis()), StandardCharsets.UTF_8));
                    if (remote.optJSONObject("files") == null) { remote = null; continue; }
                    workingBase = b;
                    prefs.edit().putString("base", b).apply();
                    break;
                } catch (Exception ignored) { remote = null; }
            }
            if (remote == null) return;   // offline, or the online museum is not published yet

            JSONArray mirrors = remote.optJSONArray("mirrors");
            if (mirrors != null) prefs.edit().putString("mirrors", mirrors.toString()).apply();

            // 1) announcement
            try {
                JSONObject a = new JSONObject(new String(get(workingBase + "updates/announce.json?t=" + System.currentTimeMillis()), StandardCharsets.UTF_8));
                a.put("type", "announce");
                toPage(a);
            } catch (Exception ignored) { }

            // 2) newer museum files
            try {
                JSONObject local = currentManifest();
                if (remote.optLong("version") > local.optLong("version")) {
                    JSONObject lf = local.optJSONObject("files"), rf = remote.getJSONObject("files");
                    if (lf == null) lf = new JSONObject();
                    List<String> changed = new ArrayList<>();
                    long bytes = 0;
                    for (Iterator<String> it = rf.keys(); it.hasNext(); ) {
                        String p = it.next();
                        if (!SAFE_PATH.matcher(p).matches() || p.equals("manifest.json")) continue;
                        JSONArray r = rf.getJSONArray(p), l = lf.optJSONArray(p);
                        if (l == null || !r.getString(0).equals(l.optString(0))) { changed.add(p); bytes += r.optLong(1); }
                    }
                    if (changed.isEmpty()) {
                        saveManifest(remote, lf);
                    } else {
                        waitingManifest = remote; waitingFiles = changed; waitingBytes = bytes;
                        if (bytes <= ASK_ABOVE_BYTES) download();
                        else toPage(new JSONObject().put("type", "content").put("state", "ask")
                                .put("mb", Math.round(bytes / 104857.6) / 10.0).put("count", changed.size()));
                    }
                }
            } catch (Exception ignored) { }

            // 3) newer app (only needed when the app itself changes)
            try {
                JSONObject app = new JSONObject(new String(get(workingBase + "updates/app.json?t=" + System.currentTimeMillis()), StandardCharsets.UTF_8));
                if (app.optLong("versionCode") > myVersionCode()) { app.put("type", "app"); toPage(app); }
            } catch (Exception ignored) { }
        });
    }

    private void download() {
        final JSONObject remote = waitingManifest;
        final List<String> files = waitingFiles;
        final long total = Math.max(1, waitingBytes);
        if (remote == null || files == null || busy) return;
        busy = true;
        bg.execute(() -> {
            File tmp = new File(liveDir, ".tmp");
            deleteTree(tmp);
            try {
                JSONObject rf = remote.getJSONObject("files");
                long[] done = {0};
                long[] lastPost = {0};
                List<String> order = bases();
                if (workingBase != null) { order.remove(workingBase); order.add(0, workingBase); }
                for (String p : files) {
                    String want = rf.getJSONArray(p).getString(0);
                    File out = new File(tmp, p);
                    boolean ok = false;
                    for (String b : order) {
                        long before = done[0];
                        try {
                            if (fetchTo(b + encodePath(p), out, want, n -> {
                                done[0] += n;
                                long now = System.currentTimeMillis();
                                if (now - lastPost[0] > 400) {
                                    lastPost[0] = now;
                                    try { toPage(new JSONObject().put("type", "content").put("state", "progress").put("p", Math.min(99, done[0] * 100 / total))); } catch (Exception ignored) { }
                                }
                            })) { ok = true; break; }
                        } catch (Exception ignored) { }
                        done[0] = before;
                    }
                    if (!ok) throw new IOException("could not get " + p);
                }
                // all files arrived and match: put them in place
                for (String p : files) {
                    File src = new File(tmp, p), dst = new File(liveDir, p);
                    File parent = dst.getParentFile();
                    if (parent != null) parent.mkdirs();
                    if (dst.exists()) dst.delete();
                    if (!src.renameTo(dst)) throw new IOException("could not place " + p);
                }
                deleteTree(tmp);
                JSONObject old = currentManifest().optJSONObject("files");
                saveManifest(remote, old);
                waitingManifest = null; waitingFiles = null;
                toPage(new JSONObject().put("type", "content").put("state", "done").put("count", files.size()));
            } catch (Exception e) {
                deleteTree(tmp);
                try { toPage(new JSONObject().put("type", "content").put("state", "error")); } catch (Exception ignored) { }
            } finally {
                busy = false;
            }
        });
    }

    /** Remembers the new list of files; downloaded files that no longer exist online are removed. */
    private void saveManifest(JSONObject remote, JSONObject oldFiles) throws Exception {
        liveDir.mkdirs();
        JSONObject rf = remote.getJSONObject("files");
        if (oldFiles != null) {
            for (Iterator<String> it = oldFiles.keys(); it.hasNext(); ) {
                String p = it.next();
                if (!rf.has(p) && SAFE_PATH.matcher(p).matches()) new File(liveDir, p).delete();
            }
        }
        File f = new File(liveDir, "manifest.json"), t = new File(liveDir, "manifest.json.part");
        try (OutputStream o = new FileOutputStream(t)) { o.write(remote.toString().getBytes(StandardCharsets.UTF_8)); }
        if (f.exists()) f.delete();
        t.renameTo(f);
    }

    /* ---------------- network helpers ---------------- */

    private interface Progress { void add(long n); }

    private static HttpURLConnection open(String url) throws IOException {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setConnectTimeout(8000);
        c.setReadTimeout(20000);
        c.setInstanceFollowRedirects(true);
        c.setRequestProperty("User-Agent", "MuseumArshiaRM/" + Build.VERSION.SDK_INT);
        c.setRequestProperty("Cache-Control", "no-cache");
        return c;
    }

    private static byte[] get(String url) throws IOException {
        HttpURLConnection c = open(url);
        try {
            if (c.getResponseCode() != 200) throw new IOException("HTTP " + c.getResponseCode());
            try (InputStream in = c.getInputStream()) { return readAll(in); }
        } finally {
            c.disconnect();
        }
    }

    private static boolean fetchTo(String url, File out, String sha1, Progress prog) throws Exception {
        File parent = out.getParentFile();
        if (parent != null) parent.mkdirs();
        HttpURLConnection c = open(url);
        MessageDigest md = MessageDigest.getInstance("SHA-1");
        try {
            if (c.getResponseCode() != 200) return false;
            try (InputStream in = c.getInputStream(); OutputStream o = new FileOutputStream(out)) {
                byte[] buf = new byte[64 * 1024];
                int n;
                while ((n = in.read(buf)) > 0) { o.write(buf, 0, n); md.update(buf, 0, n); prog.add(n); }
            }
        } finally {
            c.disconnect();
        }
        if (hex(md.digest()).equalsIgnoreCase(sha1)) return true;
        out.delete();
        return false;
    }

    private static String encodePath(String p) {
        StringBuilder sb = new StringBuilder();
        for (String part : p.split("/")) {
            if (sb.length() > 0) sb.append('/');
            sb.append(Uri.encode(part));
        }
        return sb.toString();
    }

    private static byte[] readAll(InputStream in) throws IOException {
        ByteArrayOutputStream bo = new ByteArrayOutputStream();
        byte[] buf = new byte[16 * 1024];
        int n;
        while ((n = in.read(buf)) > 0) bo.write(buf, 0, n);
        return bo.toByteArray();
    }

    private static String hex(byte[] b) {
        StringBuilder sb = new StringBuilder();
        for (byte x : b) sb.append(String.format(Locale.ROOT, "%02x", x));
        return sb.toString();
    }

    private static void deleteTree(File f) {
        if (f == null || !f.exists()) return;
        File[] kids = f.listFiles();
        if (kids != null) for (File k : kids) deleteTree(k);
        f.delete();
    }

    @SuppressWarnings("deprecation")
    private long myVersionCode() {
        try {
            PackageInfo pi = getPackageManager().getPackageInfo(getPackageName(), 0);
            return Build.VERSION.SDK_INT >= 28 ? pi.getLongVersionCode() : pi.versionCode;
        } catch (Exception e) {
            return 0;
        }
    }

    private String myVersionName() {
        try {
            return getPackageManager().getPackageInfo(getPackageName(), 0).versionName;
        } catch (Exception e) {
            return "";
        }
    }

    /* ---------------- talking to the museum page ---------------- */

    private void toPage(JSONObject msg) {
        final String js = "window.museumFromApp&&window.museumFromApp(" + msg.toString() + ")";
        ui.post(() -> { if (web != null) web.evaluateJavascript(js, null); });
    }

    private void openOutside(String url) {
        if (url == null || !url.startsWith("https://")) return;
        try {
            startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(url)));
        } catch (Exception ignored) { }
    }

    /** What the museum page can ask the app to do. */
    private class Bridge {
        @JavascriptInterface public void downloadUpdate() { download(); }
        @JavascriptInterface public void reload() { ui.post(() -> { if (web != null) web.loadUrl(START_URL); }); }
        @JavascriptInterface public void openUrl(String url) { ui.post(() -> openOutside(url)); }
        @JavascriptInterface public String version() { return myVersionName(); }
        @JavascriptInterface public String contentVersion() { return String.valueOf(currentManifest().optLong("version")); }
    }

    /* ---------------- full screen ---------------- */

    @SuppressWarnings("deprecation")
    private void hideBars() {
        getWindow().getDecorView().setSystemUiVisibility(
                View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                        | View.SYSTEM_UI_FLAG_FULLSCREEN
                        | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                        | View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                        | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                        | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN);
    }

    @Override
    public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        if (hasFocus) hideBars();
    }

    @Override
    protected void onPause() {
        super.onPause();
        if (web != null) web.onPause();
    }

    @Override
    protected void onResume() {
        super.onResume();
        if (web != null) {
            web.onResume();
            if (lastCheck > 0 && System.currentTimeMillis() - lastCheck > RECHECK_MS) checkOnline();
        }
        hideBars();
    }

    @Override
    protected void onDestroy() {
        bg.shutdownNow();
        if (web != null) web.destroy();
        web = null;
        super.onDestroy();
    }

    @SuppressWarnings("deprecation")
    @Override
    public void onBackPressed() {
        finish();
    }
}
