package com.neko233.launcher;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.BroadcastReceiver;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.pm.ApplicationInfo;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.content.res.Configuration;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.LinearGradient;
import android.graphics.Paint;
import android.graphics.RadialGradient;
import android.graphics.Shader;
import android.graphics.Typeface;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.GradientDrawable;
import android.net.Uri;
import android.os.Bundle;
import android.provider.Settings;
import android.text.Editable;
import android.text.TextUtils;
import android.text.TextWatcher;
import android.util.DisplayMetrics;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.Window;
import android.view.WindowManager;
import android.widget.BaseAdapter;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.GridView;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.Comparator;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;

/**
 * Custom slim, 100% ad-free MuMu-style Android desktop for NekoSimulator233.
 * - Immediately displays newly installed user apps (TapTap, games, APKs) first
 * - Hides stock phone/GMS bloatware from the launcher grid
 * - Zero promotional feeds, zero ad banners, zero search ad keywords
 */
public class LauncherActivity extends Activity {

    private static final int WHITE = Color.parseColor("#FFFFFFFF");
    private static final int MUTED = Color.parseColor("#9EB2D0");
    private static final int SEARCH_BG = Color.parseColor("#4418263E");
    private static final int DOCK_BG = Color.parseColor("#66111C30");
    private static final int ICON_CARD_BG = Color.parseColor("#28FFFFFF");

    private static final Set<String> HIDDEN_BLOAT = new HashSet<>(Arrays.asList(
            "com.neko233.launcher",
            "com.google.android.apps.nexuslauncher",
            "com.android.launcher3",
            "com.google.android.googlequicksearchbox",
            "com.google.android.dialer",
            "com.android.dialer",
            "com.google.android.apps.messaging",
            "com.android.messaging",
            "com.google.android.contacts",
            "com.android.contacts",
            "com.google.android.youtube",
            "com.google.android.apps.youtube.music",
            "com.google.android.apps.maps",
            "com.google.android.gm",
            "com.google.android.videos",
            "com.google.android.apps.tachyon",
            "com.google.android.apps.docs",
            "com.google.android.calendar",
            "com.android.calendar",
            "com.google.android.apps.wellbeing",
            "com.android.stk",
            "com.google.android.apps.safetyhub",
            "com.google.android.calculator",
            "com.google.android.deskclock"
    ));

    private PackageManager pm;
    private List<ResolveInfo> allApps = new ArrayList<>();
    private List<ResolveInfo> filteredApps = new ArrayList<>();
    private BaseAdapter gridAdapter;
    private GridView gridView;
    private LinearLayout dockContainer;
    private String currentQuery = "";

    private final BroadcastReceiver pkgReceiver = new BroadcastReceiver() {
        @Override
        public void onReceive(Context context, Intent intent) {
            refreshApps();
        }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        pm = getPackageManager();

        Window w = getWindow();
        w.addFlags(WindowManager.LayoutParams.FLAG_DRAWS_SYSTEM_BAR_BACKGROUNDS);
        w.setStatusBarColor(Color.TRANSPARENT);
        w.setNavigationBarColor(Color.parseColor("#080E1B"));

        FrameLayout frame = new FrameLayout(this);
        frame.addView(new StarryBackdropView(this), new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setFocusable(true);
        root.setFocusableInTouchMode(true);
        root.setPadding(dp(20), dp(14), dp(20), dp(16));

        root.addView(buildSearchBar());
        root.addView(buildAppGrid());

        dockContainer = new LinearLayout(this);
        dockContainer.setOrientation(LinearLayout.HORIZONTAL);
        dockContainer.setGravity(Gravity.CENTER);
        dockContainer.setBackground(rounded(DOCK_BG, dp(24), Color.parseColor("#26FFFFFF")));
        dockContainer.setPadding(dp(14), dp(10), dp(14), dp(10));
        LinearLayout.LayoutParams dockParams = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        dockParams.topMargin = dp(10);
        root.addView(dockContainer, dockParams);

        frame.addView(root, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        setContentView(frame);

        IntentFilter filter = new IntentFilter();
        filter.addAction(Intent.ACTION_PACKAGE_ADDED);
        filter.addAction(Intent.ACTION_PACKAGE_REMOVED);
        filter.addAction(Intent.ACTION_PACKAGE_REPLACED);
        filter.addAction(Intent.ACTION_PACKAGE_CHANGED);
        filter.addDataScheme("package");
        registerReceiver(pkgReceiver, filter);

        refreshApps();
    }

    @Override
    protected void onDestroy() {
        try {
            unregisterReceiver(pkgReceiver);
        } catch (Exception ignored) {
        }
        super.onDestroy();
    }

    @Override
    protected void onResume() {
        super.onResume();
        refreshApps();
    }

    @Override
    public void onConfigurationChanged(Configuration newConfig) {
        super.onConfigurationChanged(newConfig);
        if (gridView != null) {
            gridView.setNumColumns(newConfig.orientation == Configuration.ORIENTATION_LANDSCAPE ? 6 : 4);
        }
    }

    @Override
    public void onBackPressed() {
        // Home desktop stays open on Back.
    }

    private void refreshApps() {
        allApps = queryCleanApps();
        applyFilter(currentQuery);
        rebuildDock();
    }

    private boolean isUserInstalled(ResolveInfo info) {
        if (info == null || info.activityInfo == null || info.activityInfo.applicationInfo == null) {
            return false;
        }
        int flags = info.activityInfo.applicationInfo.flags;
        return (flags & ApplicationInfo.FLAG_SYSTEM) == 0;
    }

    private List<ResolveInfo> queryCleanApps() {
        Intent main = new Intent(Intent.ACTION_MAIN, null);
        main.addCategory(Intent.CATEGORY_LAUNCHER);
        List<ResolveInfo> found = pm.queryIntentActivities(main, 0);
        List<ResolveInfo> result = new ArrayList<>();
        Set<String> seenPkg = new HashSet<>();
        for (ResolveInfo info : found) {
            if (info.activityInfo == null) continue;
            String pkg = info.activityInfo.packageName;
            if (HIDDEN_BLOAT.contains(pkg) || seenPkg.contains(pkg)) continue;
            seenPkg.add(pkg);
            result.add(info);
        }
        Collections.sort(result, new Comparator<ResolveInfo>() {
            @Override
            public int compare(ResolveInfo a, ResolveInfo b) {
                boolean ua = isUserInstalled(a);
                boolean ub = isUserInstalled(b);
                if (ua != ub) {
                    return ua ? -1 : 1; // User-installed apps (TapTap, games) always first!
                }
                if ("com.taptap".equals(a.activityInfo.packageName)) return -1;
                if ("com.taptap".equals(b.activityInfo.packageName)) return 1;
                return labelOf(a).compareToIgnoreCase(labelOf(b));
            }
        });
        return result;
    }

    private void applyFilter(String query) {
        currentQuery = query == null ? "" : query;
        String q = currentQuery.trim().toLowerCase(Locale.ROOT);
        filteredApps.clear();
        for (ResolveInfo info : allApps) {
            if (q.isEmpty()
                    || labelOf(info).toLowerCase(Locale.ROOT).contains(q)
                    || info.activityInfo.packageName.toLowerCase(Locale.ROOT).contains(q)) {
                filteredApps.add(info);
            }
        }
        if (gridAdapter != null) {
            gridAdapter.notifyDataSetChanged();
        }
    }

    private String labelOf(ResolveInfo info) {
        String pkg = info.activityInfo.packageName;
        if ("com.android.settings".equals(pkg)) return "系统设置";
        if ("com.android.documentsui".equals(pkg) || "com.google.android.documentsui".equals(pkg)) return "文件管理";
        if ("com.android.chrome".equals(pkg)) return "浏览器";
        if ("com.android.camera2".equals(pkg)) return "相机";
        if ("com.android.vending".equals(pkg)) return "Play 商店";
        CharSequence c = info.loadLabel(pm);
        return c == null ? pkg : c.toString();
    }

    private View buildSearchBar() {
        EditText search = new EditText(this);
        search.setHint("🔍 搜索本机应用与游戏（纯净无广告）");
        search.setHintTextColor(MUTED);
        search.setTextColor(WHITE);
        search.setTextSize(14);
        search.setSingleLine(true);
        search.setPadding(dp(18), dp(10), dp(18), dp(10));
        search.setBackground(rounded(SEARCH_BG, dp(22), Color.parseColor("#2AFFFFFF")));
        search.addTextChangedListener(new TextWatcher() {
            @Override public void beforeTextChanged(CharSequence s, int start, int count, int after) {}
            @Override public void onTextChanged(CharSequence s, int start, int before, int count) {
                applyFilter(s == null ? "" : s.toString());
            }
            @Override public void afterTextChanged(Editable s) {}
        });
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        p.bottomMargin = dp(18);
        search.setLayoutParams(p);
        return search;
    }

    private View buildAppGrid() {
        gridView = new GridView(this);
        boolean land = getResources().getConfiguration().orientation == Configuration.ORIENTATION_LANDSCAPE;
        gridView.setNumColumns(land ? 6 : 4);
        gridView.setVerticalSpacing(dp(18));
        gridView.setHorizontalSpacing(dp(12));
        gridView.setStretchMode(GridView.STRETCH_COLUMN_WIDTH);
        gridView.setSelector(android.R.color.transparent);

        gridAdapter = new BaseAdapter() {
            @Override public int getCount() { return filteredApps.size(); }
            @Override public Object getItem(int i) { return filteredApps.get(i); }
            @Override public long getItemId(int i) { return i; }
            @Override public View getView(int i, View reuse, ViewGroup parent) {
                final ResolveInfo info = filteredApps.get(i);
                LinearLayout cell = new LinearLayout(LauncherActivity.this);
                cell.setOrientation(LinearLayout.VERTICAL);
                cell.setGravity(Gravity.CENTER);
                cell.setPadding(dp(6), dp(8), dp(6), dp(8));

                FrameLayout iconWrap = new FrameLayout(LauncherActivity.this);
                iconWrap.setBackground(rounded(ICON_CARD_BG, dp(16), 0));
                int box = dp(58);
                LinearLayout.LayoutParams wrapLP = new LinearLayout.LayoutParams(box, box);
                iconWrap.setLayoutParams(wrapLP);

                ImageView icon = new ImageView(LauncherActivity.this);
                Drawable d = info.loadIcon(pm);
                if (d != null) icon.setImageDrawable(d);
                icon.setScaleType(ImageView.ScaleType.FIT_CENTER);
                FrameLayout.LayoutParams imgLP = new FrameLayout.LayoutParams(dp(48), dp(48), Gravity.CENTER);
                iconWrap.addView(icon, imgLP);
                cell.addView(iconWrap);

                TextView tv = new TextView(LauncherActivity.this);
                tv.setText(labelOf(info));
                tv.setTextSize(12);
                tv.setTextColor(WHITE);
                tv.setMaxLines(1);
                tv.setEllipsize(TextUtils.TruncateAt.END);
                tv.setGravity(Gravity.CENTER);
                tv.setShadowLayer(4f, 0f, 1f, Color.parseColor("#AA000000"));
                LinearLayout.LayoutParams tvLP = new LinearLayout.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
                tvLP.topMargin = dp(6);
                cell.addView(tv, tvLP);

                cell.setOnClickListener(new View.OnClickListener() {
                    @Override
                    public void onClick(View v) {
                        launchResolveInfo(info);
                    }
                });
                cell.setOnLongClickListener(new View.OnLongClickListener() {
                    @Override
                    public boolean onLongClick(View v) {
                        showAppMenu(info);
                        return true;
                    }
                });
                return cell;
            }
        };
        gridView.setAdapter(gridAdapter);
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f);
        gridView.setLayoutParams(p);
        return gridView;
    }

    private void launchResolveInfo(ResolveInfo info) {
        try {
            Intent intent = new Intent(Intent.ACTION_MAIN);
            intent.addCategory(Intent.CATEGORY_LAUNCHER);
            intent.setComponent(new ComponentName(info.activityInfo.packageName, info.activityInfo.name));
            intent.setFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_RESET_TASK_IF_NEEDED);
            startActivity(intent);
        } catch (Exception ignored) {
        }
    }

    private void showAppMenu(final ResolveInfo info) {
        final String pkg = info.activityInfo.packageName;
        final boolean userApp = isUserInstalled(info);
        final String[] items = userApp
                ? new String[]{"打开应用", "应用详情", "卸载应用"}
                : new String[]{"打开应用", "应用详情"};
        new AlertDialog.Builder(this)
                .setTitle(labelOf(info))
                .setItems(items, (dialog, which) -> {
                    if (which == 0) {
                        launchResolveInfo(info);
                    } else if (which == 1) {
                        Intent detail = new Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                                Uri.parse("package:" + pkg));
                        detail.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                        startActivity(detail);
                    } else if (which == 2 && userApp) {
                        Intent del = new Intent(Intent.ACTION_DELETE, Uri.parse("package:" + pkg));
                        del.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                        startActivity(del);
                    }
                })
                .show();
    }

    private void rebuildDock() {
        if (dockContainer == null) return;
        dockContainer.removeAllViews();
        String storePkg = isInstalled("com.taptap") ? "com.taptap" : "com.android.vending";
        String filesPkg = isInstalled("com.google.android.documentsui")
                ? "com.google.android.documentsui" : "com.android.documentsui";
        String[] dockPkgs = new String[]{
                storePkg,
                "com.android.chrome",
                filesPkg,
                "com.android.camera2",
                "com.android.settings"
        };
        for (final String pkg : dockPkgs) {
            final Intent launch = pm.getLaunchIntentForPackage(pkg);
            if (launch == null) continue;
            FrameLayout wrap = new FrameLayout(this);
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(0, dp(52), 1f);
            wrap.setLayoutParams(lp);

            ImageView iv = new ImageView(this);
            try {
                iv.setImageDrawable(pm.getApplicationIcon(pkg));
            } catch (Exception ignored) {
            }
            iv.setScaleType(ImageView.ScaleType.FIT_CENTER);
            wrap.addView(iv, new FrameLayout.LayoutParams(dp(44), dp(44), Gravity.CENTER));
            wrap.setOnClickListener(v -> {
                launch.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_RESET_TASK_IF_NEEDED);
                try {
                    startActivity(launch);
                } catch (Exception ignored) {
                }
            });
            dockContainer.addView(wrap);
        }
    }

    private boolean isInstalled(String pkg) {
        try {
            pm.getPackageInfo(pkg, 0);
            return true;
        } catch (Exception e) {
            return false;
        }
    }

    private GradientDrawable rounded(int fill, float radiusPx, int strokeColor) {
        GradientDrawable d = new GradientDrawable();
        d.setColor(fill);
        d.setCornerRadius(radiusPx);
        if (strokeColor != 0) {
            d.setStroke(Math.max(1, dp(1)), strokeColor);
        }
        return d;
    }

    private int dp(int v) {
        DisplayMetrics dm = getResources().getDisplayMetrics();
        return Math.round(v * dm.density);
    }

    /**
     * Lightweight procedural starry space wallpaper (MuMu cosmic style, zero ads, zero bitmap RAM).
     */
    private static final class StarryBackdropView extends View {
        private final Paint bgPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
        private final Paint glowPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
        private final Paint starPaint = new Paint(Paint.ANTI_ALIAS_FLAG);

        StarryBackdropView(Context ctx) {
            super(ctx);
            starPaint.setColor(Color.WHITE);
        }

        @Override
        protected void onDraw(Canvas c) {
            int w = getWidth();
            int h = getHeight();
            if (w <= 0 || h <= 0) return;

            bgPaint.setShader(new LinearGradient(0, 0, w, h,
                    new int[]{Color.parseColor("#0A1328"), Color.parseColor("#0F2347"), Color.parseColor("#080E1C")},
                    new float[]{0f, 0.55f, 1f}, Shader.TileMode.CLAMP));
            c.drawRect(0, 0, w, h, bgPaint);

            float gx = w * 0.72f;
            float gy = h * 0.68f;
            float gr = Math.min(w, h) * 0.55f;
            glowPaint.setShader(new RadialGradient(gx, gy, gr,
                    Color.parseColor("#382B7DE9"), Color.TRANSPARENT, Shader.TileMode.CLAMP));
            c.drawCircle(gx, gy, gr, glowPaint);

            // Deterministic subtle starfield
            long seed = 233233L;
            for (int i = 0; i < 42; i++) {
                seed = (seed * 1103515245L + 12345L) & 0x7fffffffL;
                float sx = (seed % w);
                seed = (seed * 1103515245L + 12345L) & 0x7fffffffL;
                float sy = (seed % (int) (h * 0.85f));
                int alpha = 50 + (int) (seed % 130);
                starPaint.setAlpha(alpha);
                float r = (i % 5 == 0) ? 2.2f : 1.3f;
                c.drawCircle(sx, sy, r, starPaint);
            }
        }
    }
}
