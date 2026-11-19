package com.neko233.launcher;

import android.app.Activity;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ApplicationInfo;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.Drawable;
import android.net.Uri;
import android.os.Bundle;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.GridView;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

import java.util.ArrayList;
import java.util.Collections;
import java.util.Comparator;
import java.util.List;

/**
 * Neko home screen for the Android emulator. Styled like a PC-emulator
 * launcher: live wallpaper, real app icons, a search bar, a game-center area
 * that opens the installed app store / TapTap, and a dock.
 *
 * The entire UI is built in code so the APK needs no resource-compiler inputs.
 */
public class LauncherActivity extends Activity {

    private static final int WHITE = Color.parseColor("#FFFFFFFF");
    private static final int MUTED = Color.parseColor("#D0DCF0");
    private static final int CARD = Color.parseColor("#2A000000");
    private static final int ACCENT = Color.parseColor("#4FB3FF");

    private PackageManager pm;
    private List<ResolveInfo> apps;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        pm = getPackageManager();
        apps = loadApps();
        getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_FULLSCREEN);

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        Drawable wallpaper = loadWallpaper();
        if (wallpaper != null) {
            root.setBackground(wallpaper);
        } else {
            root.setBackgroundColor(Color.parseColor("#0B1B39"));
        }
        root.setPadding(48, 28, 48, 28);

        root.addView(header());
        root.addView(searchBar());
        root.addView(featureRow());
        root.addView(appGrid());
        LinearLayout dock = dock();
        LinearLayout.LayoutParams dockParams =
                new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f);
        dockParams.gravity = Gravity.BOTTOM;
        root.addView(dock, dockParams);
        setContentView(root);
    }

    @Override
    public void onBackPressed() {
        // Home screens do not close on Back.
    }

    /** Real wallpaper from Android, never a hard-coded letter. */
    private Drawable loadWallpaper() {
        try {
            return android.app.WallpaperManager.getInstance(this).getDrawable();
        } catch (Exception ignored) {
            return null;
        }
    }

    private List<ResolveInfo> loadApps() {
        Intent main = new Intent(Intent.ACTION_MAIN, null);
        main.addCategory(Intent.CATEGORY_LAUNCHER);
        List<ResolveInfo> found = pm.queryIntentActivities(main, 0);
        List<ResolveInfo> result = new ArrayList<>();
        for (ResolveInfo info : found) {
            if (info.activityInfo == null) continue;
            if ("com.neko233.launcher".equals(info.activityInfo.packageName)) continue;
            result.add(info);
        }
        Collections.sort(result, new Comparator<ResolveInfo>() {
            @Override public int compare(ResolveInfo a, ResolveInfo b) {
                return label(a).compareToIgnoreCase(label(b));
            }
        });
        return result;
    }

    private String label(ResolveInfo info) {
        CharSequence c = info.loadLabel(pm);
        return c == null ? info.activityInfo.name : c.toString();
    }

    private View header() {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        TextView brand = text("NekoSimulator233", 30, WHITE, true);
        brand.setShadowLayer(6, 2, 2, Color.parseColor("#AA000000"));
        row.addView(brand, new LinearLayout.LayoutParams(0, 84, 1f));
        TextView clock = text("桌面", 18, MUTED, false);
        clock.setGravity(Gravity.END);
        row.addView(clock, new LinearLayout.LayoutParams(0, 84, 1f));
        return row;
    }

    private View searchBar() {
        TextView search = text("  搜索应用、游戏…", 18, WHITE, false);
        search.setBackgroundColor(CARD);
        search.setGravity(Gravity.CENTER_VERTICAL);
        search.setOnClickListener(v -> {
            Intent intent = new Intent(Intent.ACTION_VIEW, Uri.parse("market://search?q=game"));
            intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            try { startActivity(intent); } catch (Exception ignored) { }
        });
        LinearLayout.LayoutParams p =
                new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 74);
        p.topMargin = 10;
        p.bottomMargin = 14;
        search.setLayoutParams(p);
        return search;
    }

    private View featureRow() {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.addView(card("游戏中心", "TapTap · 精选手游", firstInstalled("com.taptap", "com.taptap.global", "com.android.vending")));
        row.addView(card("每日新发现", "Google Play 热门推荐", "com.android.vending"));
        row.addView(card("系统设置", "分辨率 · 键位 · 应用", "com.android.settings"));
        return row;
    }

    /** Picks the first installed package from candidates, falling back to a store. */
    private String firstInstalled(String... packages) {
        for (String pkg : packages) {
            try {
                pm.getPackageInfo(pkg, 0);
                return pkg;
            } catch (Exception ignored) { }
        }
        return "com.android.vending";
    }

    private View card(String title, String subtitle, final String pkg) {
        LinearLayout c = new LinearLayout(this);
        c.setOrientation(LinearLayout.HORIZONTAL);
        c.setBackgroundColor(CARD);
        c.setPadding(20, 16, 20, 16);
        c.setGravity(Gravity.CENTER_VERTICAL);
        ImageView icon = new ImageView(this);
        Drawable d = safeIcon(pkg);
        if (d != null) icon.setImageDrawable(d);
        icon.setAdjustViewBounds(true);
        icon.setMaxWidth(72);
        icon.setMaxHeight(72);
        c.addView(icon, new LinearLayout.LayoutParams(72, 72));
        LinearLayout labels = new LinearLayout(this);
        labels.setOrientation(LinearLayout.VERTICAL);
        labels.addView(text(title, 22, WHITE, true));
        labels.addView(text(subtitle, 14, MUTED, false));
        c.addView(labels, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
        c.setOnClickListener(v -> launch(pkg));
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(0, 150, 1f);
        p.setMargins(0, 0, 14, 14);
        c.setLayoutParams(p);
        return c;
    }

    private Drawable safeIcon(String pkg) {
        try {
            return pm.getApplicationIcon(pkg);
        } catch (Exception ignored) {
            return null;
        }
    }

    private View appGrid() {
        GridView grid = new GridView(this);
        grid.setNumColumns(7);
        grid.setVerticalSpacing(18);
        grid.setHorizontalSpacing(10);
        grid.setAdapter(new BaseAdapter() {
            @Override public int getCount() { return apps.size(); }
            @Override public Object getItem(int i) { return apps.get(i); }
            @Override public long getItemId(int i) { return i; }
            @Override public View getView(int i, View reuse, ViewGroup parent) {
                final ResolveInfo info = apps.get(i);
                LinearLayout tile = new LinearLayout(LauncherActivity.this);
                tile.setOrientation(LinearLayout.VERTICAL);
                tile.setGravity(Gravity.CENTER);
                tile.setBackgroundColor(CARD);
                tile.setPadding(6, 12, 6, 10);
                ImageView icon = new ImageView(LauncherActivity.this);
                Drawable d = info.loadIcon(pm);
                if (d != null) icon.setImageDrawable(d);
                icon.setAdjustViewBounds(true);
                icon.setMaxWidth(86);
                icon.setMaxHeight(86);
                tile.addView(icon, new LinearLayout.LayoutParams(86, 86));
                TextView label = text(label(info), 14, WHITE, false);
                label.setMaxLines(1);
                tile.addView(label);
                tile.setOnClickListener(v -> {
                    Intent intent = pm.getLaunchIntentForPackage(info.activityInfo.packageName);
                    if (intent != null) {
                        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                        startActivity(intent);
                    }
                });
                return tile;
            }
        });
        LinearLayout.LayoutParams p =
                new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 0, 4f);
        p.topMargin = 6;
        grid.setLayoutParams(p);
        return grid;
    }

    private LinearLayout dock() {
        LinearLayout dock = new LinearLayout(this);
        dock.setOrientation(LinearLayout.HORIZONTAL);
        dock.setGravity(Gravity.CENTER);
        dock.setBackgroundColor(CARD);
        dock.setPadding(16, 14, 16, 14);
        for (String pkg : new String[]{"com.android.settings", "com.android.vending", "com.android.chrome"}) {
            dock.addView(dockIcon(pkg));
        }
        return dock;
    }

    private View dockIcon(String pkg) {
        LinearLayout b = new LinearLayout(this);
        b.setOrientation(LinearLayout.HORIZONTAL);
        b.setGravity(Gravity.CENTER);
        b.setBackgroundColor(ACCENT);
        ImageView icon = new ImageView(this);
        Drawable d = safeIcon(pkg);
        if (d != null) icon.setImageDrawable(d);
        icon.setAdjustViewBounds(true);
        icon.setMaxWidth(56);
        icon.setMaxHeight(56);
        b.addView(icon, new LinearLayout.LayoutParams(56, 56));
        b.addView(text(labelForPkg(pkg), 16, WHITE, true));
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(210, 72);
        p.setMargins(18, 0, 18, 0);
        b.setLayoutParams(p);
        b.setOnClickListener(v -> launch(pkg));
        return b;
    }

    private String labelForPkg(String pkg) {
        try {
            ApplicationInfo app = pm.getApplicationInfo(pkg, 0);
            CharSequence c = pm.getApplicationLabel(app);
            return c == null ? pkg : c.toString();
        } catch (Exception ignored) {
            if ("com.android.chrome".equals(pkg)) return "浏览器";
            if ("com.android.vending".equals(pkg)) return "商店";
            return "设置";
        }
    }

    private TextView text(String value, float size, int color, boolean bold) {
        TextView t = new TextView(this);
        t.setText(value);
        t.setTextColor(color);
        t.setTextSize(size);
        if (bold) t.setTypeface(Typeface.DEFAULT_BOLD);
        t.setPadding(4, 2, 4, 2);
        return t;
    }

    private void launch(String pkg) {
        try {
            Intent intent = pm.getLaunchIntentForPackage(pkg);
            if (intent == null) {
                intent = new Intent(Intent.ACTION_VIEW, Uri.parse("market://details?id=" + pkg));
            }
            intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            startActivity(intent);
        } catch (Exception ignored) {
            // Missing apps are simply no-ops on the home screen.
        }
    }
}
