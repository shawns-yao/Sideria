import { test, expect } from "@playwright/test";
for (const size of [
  { width: 1440, height: 900 },
  { width: 1920, height: 1080 },
  { width: 390, height: 844 },
]) {
  test(`demo layout and honest states ${size.width}`, async ({ page }) => {
    await page.setViewportSize(size);
    const requests: string[] = [];
    page.on("request", (r) => {
      if (r.url().includes("/api/") || r.url().includes("/agent/"))
        requests.push(r.url());
    });
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.goto("/?demo=1");
    await expect(
      page.getByRole("heading", { name: "CPU", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Memory", exact: true }),
    ).toBeVisible();
    if (size.width >= 1440) {
      for (const name of ["Disk", "Network"]) {
        const box = await page
          .getByRole("heading", { name, exact: true })
          .boundingBox();
        expect(box!.y + box!.height).toBeLessThan(size.height);
      }
    }
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.getByLabel("挂载点", { exact: true }).selectOption("/data");
    await expect(page.locator(".disk .center").first()).toContainText(
      "12.0 GiB",
    );
    await page.getByLabel("网卡", { exact: true }).selectOption("lo");
    await expect(page.locator(".network-rates")).toContainText("0 B");
    await page.screenshot({
      path: `test-results/dashboard-${size.width}.png`,
      fullPage: true,
    });
    if (size.width < 700)
      await page.getByRole("button", { name: "打开导航" }).click();
    await page
      .getByRole("button", { name: "Singapore-02 · 失联样本", exact: true })
      .click();
    await expect(
      page.getByText(
        "数据已过期，下方数值是上次采样。Agent 失联或采集延迟不代表服务器关机。",
      ),
    ).toBeVisible();
    if (size.width < 700)
      await page.getByRole("button", { name: "打开导航" }).click();
    await page
      .getByRole("button", { name: "New host · 无数据", exact: true })
      .click();
    await expect(page.locator(".gauge-number strong")).toHaveText("—");
    await page.getByRole("button", { name: "分析状态", exact: true }).click();
    await expect(
      page.getByText("演示模式：未连接模型，不生成模拟回答。"),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "开始分析" })).toBeDisabled();
    expect(errors).toEqual([]);
    expect(requests).toEqual([]);
  });
}
test("keyboard and reduced motion", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/?demo=1");
  await page.keyboard.press("Tab");
  await expect(page.getByText("跳到主要内容")).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("#main")).toBeFocused();
  await expect(page.locator(".status-dot").first()).toHaveCSS(
    "animation-name",
    "none",
  );
});

test("late read response cannot overwrite another host selection", async ({
  page,
}) => {
  const { demoHosts } = await import("../src/demo/hosts");
  const hosts = demoHosts.slice(0, 2).map((h) => ({ ...h, online: true }));
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/config")
      return route.fulfill({ json: { ai_enabled: false } });
    if (url.pathname === "/api/hosts") return route.fulfill({ json: hosts });
    if (url.pathname.endsWith("/query")) {
      const body = route.request().postDataJSON();
      if (body.action === "file.list")
        return route.fulfill({
          json: {
            data: {
              entries: [
                {
                  name: "evidence.txt",
                  directory: false,
                  size: 8,
                  mode: "-rw-------",
                },
              ],
              more: false,
            },
          },
        });
      if (url.pathname.includes(hosts[0]!.id)) {
        await new Promise((resolve) => setTimeout(resolve, 700));
        await route
          .fulfill({
            json: { data: { text: "OLD HOST EVIDENCE", truncated: false } },
          })
          .catch(() => {});
        return;
      }
      return route.fulfill({
        json: { data: { text: "CURRENT HOST EVIDENCE", truncated: false } },
      });
    }
    return route.fulfill({
      status: 404,
      json: { error: "fixture route not configured" },
    });
  });
  await page.goto("/");
  await page.getByRole("tab", { name: "文件", exact: true }).click();
  await page.getByRole("button", { name: /evidence.txt/ }).click();
  await page.getByRole("button", { name: hosts[1]!.name, exact: true }).click();
  await page.getByRole("button", { name: /evidence.txt/ }).click();
  await expect(page.locator(".file-preview pre")).toContainText(
    "CURRENT HOST EVIDENCE",
  );
  await page.waitForTimeout(900);
  await expect(page.locator(".file-preview pre")).not.toContainText(
    "OLD HOST EVIDENCE",
  );
});
