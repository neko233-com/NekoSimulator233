package com.neko233.launcher;

import android.app.Activity;
import android.content.Intent;
import android.content.pm.ApplicationInfo;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.GradientDrawable;
import android.net.Uri;
import android.os.Bundle;
import android.text.Editable;
import android.text.TextWatcher;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.EditText;
import android.widget.GridView;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Comparator;
import java.util.Date;
import java.util.List;
import java.util.Locale;

/**
 * Pure ad-free Neko home screen for Android.
 * Zero ad banners, zero discovery feeds, zero promotional red dots.
 */
public class LauncherActivity extends Activity {

    private static final int WHITE = Color.parseColor("#FFFFFFFF");
    private static final int MUTED = Color.parseColor("#C8D4E8");
    private static final int CARD = Color.parseColor("#38121926");
    private static final int DOCK_BG = Color.parseColor("#550E1522");
    private static final int ACCENT = Color.parseColor("#2B7DE9");

    private PackageManager pm;
    private List<ResolveInfo> allApps = new ArrayList<>();
    private List<ResolveInfo> filteredApps = new ArrayList<>();
    private BaseAdapter gridAdapter;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        pm = getPackageManager();
        allApps = loadApps();
        filteredApps = new ArrayList<>(allApps);
        getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_FULLSCREEN);

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        Drawable wallpaper = loadWallpaper();
        if (wallpaper != null) {
            root.setBackground(wallpaper);
        } else {
            GradientDrawable bg = new GradientDrawable(
                    GradientDrawable.Orientation.TL_BR,
                    new int[]{Color.parseColor("#0B162C"), Color.parseColor("#132A4A"), Color.parseColor("#0A1222")}
            );
            root.setBackground(bg);
        }
        root.setPadding(44, 28, 44, 24);

        root.addView(header());
        root.addView(localSearchBar());
        root.addView(appGrid());

        LinearLayout dock = dock();
        LinearLayout.LayoutParams dockParams =
                new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        dockParams.topMargin = 14;
        dockParams.gravity = Gravity.BOTTOM;
        root.addView(dock, dockParams);

        setContentView(root);
    }

    @Override
    protected void onResume() {
        super.onResume();
        allApps = loadApps();
        filterApps("");
    }

    @Override
    public void onBackPressed() {
        // Home screens do not close on Back.
    }

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
            @Override
            public int compare(ResolveInfo a, ResolveInfo b) {
                return label(a).compareToIgnoreCase(label(b));
            }
        });
        return result;
    }

    private void filterApps(String query) {
        String q = query == null ? "" : query.trim().toLowerCase(Locale.ROOT);
        filteredApps.clear();
        for (ResolveInfo info : allApps) {
            if (q.isEmpty()
                    || label(info).toLowerCase(Locale.ROOT).contains(q)
                    || info.activityInfo.packageName.toLowerCase(Locale.ROOT).contains(q)) {
                filteredApps.add(info);
            }
        }
        if (gridAdapter != null) {
            gridAdapter.notifyDataSetChanged();
        }
    }

    private String label(ResolveInfo info) {
        CharSequence c = info.loadLabel(pm);
        return c == null ? info.activityInfo.name : c.toString();
    }

    private GradientDrawable rounded(int fillColor, float radiusPx) {
        GradientDrawable d = new GradientDrawable();
        d.setColor(fillColor);
        d.setCornerRadius(radiusPx);
        return d;
    }

    private View header() {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);

        TextView brand = text("NekoSimulator233", 24, WHITE, true);
        brand.setShadowLayer(6, 1, 2, Color.parseColor("#99000000"));
        row.addView(brand, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f));

        String now = new SimpleDateFormat("HH:mm", Locale.getDefault()).format(new Date());
        TextView clock = text(now, 18, MUTED, false);
        clock.setGravity(Gravity.END);
        row.addView(clock, new LinearLayout.LayoutParams(LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT));
        return row;
    }

    private View localSearchBar() {
        EditText search = new EditText(this);
        search.setHint("搜索本机已安装应用…");
        search.setHintTextColor(MUTED);
        search.setTextColor(WHITE);
        search.setTextSize(16);
        search.setSingleLine(true);
        search.setPadding(28, 16, 28, 16);
        search.setBackground(rounded(CARD, 20));
        search.addTextChangedListener(new TextWatcher() {
            @Override public void beforeTextChanged(CharSequence s, int start, int count, int after) {}
            @Override public void onTextChanged(CharSequence s, int start, int before, int count) {
                filterApps(s == null ? "" : s.toString());
            }
            @Override public void afterTextChanged(Editable s) {}
        });
        LinearLayout.LayoutParams p =
                new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        p.topMargin = 14;
        p.bottomMargin = 18;
        search.setLayoutParams(p);
        return search;
    }

    private View appGrid() {
        GridView grid = new GridView(this);
        grid.setNumColumns(5);
        grid.setVerticalSpacing(16);
        grid.setHorizontalSpacing(14);
        gridAdapter = new BaseAdapter() {
            @Override public int getCount() { return filteredApps.size(); }
            @Override public Object getItem(int i) { return filteredApps.get(i); }
            @Override public long getItemId(int i) { return i; }
            @Override public View getView(int i, View reuse, ViewGroup parent) {
                final ResolveInfo info = filteredApps.get(i);
                LinearLayout tile = new LinearLayout(LauncherActivity.this);
                tile.setOrientation(LinearLayout.VERTICAL);
                tile.setGravity(Gravity.CENTER);
                tile.setBackground(rounded(CARD, 18));
                tile.setPadding(10, 14, 10, 12);

                ImageView icon = new ImageView(LauncherActivity.this);
                Drawable d = info.loadIcon(pm);
                if (d != null) icon.setImageDrawable(d);
                icon.setAdjustViewBounds(true);
                icon.setMaxWidth(84);
                icon.setMaxHeight(84);
                tile.addView(icon, new LinearLayout.LayoutParams(84, 84));

                TextView label = text(label(info), 13, WHITE, false);
                label.setMaxLines(1);
                label.setGravity(Gravity.CENTER);
                LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                        LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
                lp.topMargin = 8;
                tile.addView(label, lp);

                tile.setOnClickListener(v -> {
                    Intent intent = pm.getLaunchIntentForPackage(info.activityInfo.packageName);
                    if (intent != null) {
                        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                        startActivity(intent);
                    }
                });
                return tile;
            }
        };
        grid.setAdapter(gridAdapter);
        LinearLayout.LayoutParams p =
                new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f);
        grid.setLayoutParams(p);
        return grid;
    }

    private LinearLayout dock() {
        LinearLayout dock = new LinearLayout(this);
        dock.setOrientation(LinearLayout.HORIZONTAL);
        dock.setGravity(Gravity.CENTER);
        dock.setBackground(rounded(DOCK_BG, 24));
        dock.setPadding(16, 12, 16, 12);
        for (String pkg : new String[]{"com.android.settings", "com.android.documentsui", "com.android.chrome"}) {
            dock.addView(dockIcon(pkg));
        }
        return dock;
    }

    private View dockIcon(String pkg) {
        LinearLayout b = new LinearLayout(this);
        b.setOrientation(LinearLayout.HORIZONTAL);
        b.setGravity(Gravity.CENTER);
        b.setBackground(rounded(ACCENT, 16));
        b.setPadding(16, 10, 16, 10);

        ImageView icon = new ImageView(this);
        Drawable d = safeIcon(pkg);
        if (d != null) icon.setImageDrawable(d);
        icon.setAdjustViewBounds(true);
        icon.setMaxWidth(44);
        icon.setMaxHeight(44);
        b.addView(icon, new LinearLayout.LayoutParams(44, 44));

        TextView lbl = text(labelForPkg(pkg), 14, WHITE, true);
        lbl.setPadding(10, 0, 0, 0);
        b.addView(lbl);

        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f);
        p.setMargins(10, 0, 10, 0);
        b.setLayoutParams(p);
        b.setOnClickListener(v -> launch(pkg));
        return b;
    }

    private Drawable safeIcon(String pkg) {
        try {
            return pm.getApplicationIcon(pkg);
        } catch (Exception ignored) {
            return null;
        }
    }

    private String labelForPkg(String pkg) {
        try {
            ApplicationInfo app = pm.getApplicationInfo(pkg, 0);
            CharSequence c = pm.getApplicationLabel(app);
            return c == null ? pkg : c.toString();
        } catch (Exception ignored) {
            if ("com.android.chrome".equals(pkg)) return "浏览器";
            if ("com.android.documentsui".equals(pkg)) return "文件";
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
