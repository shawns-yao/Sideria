import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
let fixture: any;
try {
  fixture = JSON.parse(readFileSync(resolve("../.run/e2e.json"), "utf8"));
} catch {
  /* live tests require isolated fixture */
}
test.describe("real UI / center / Agent", () => {
  test.skip(!fixture, "Isolated live fixture not running");
  test.beforeEach(async ({ page }) => {
    await page.goto(fixture.base);
    await page.getByLabel("管理员访问令牌").fill(fixture.token);
    await page.getByRole("button", { name: "登录工作台" }).click();
    await page
      .getByRole("button", { name: "E2E Alpha", exact: true })
      .last()
      .click();
    await expect(page.locator(".connection")).toContainText("在线");
  });
  test("file evidence stays on the selected host and uploads/downloads through tasks", async ({
    page,
  }) => {
    await page.getByRole("tab", { name: "文件", exact: true }).click();
    await page.getByRole("button", { name: /hello.txt/ }).click();
    await expect(page.locator(".file-preview pre")).toContainText(
      "E2E Alpha file evidence",
    );
    await page
      .getByRole("button", { name: "E2E Beta", exact: true })
      .last()
      .click();
    await page.getByRole("button", { name: /hello.txt/ }).click();
    await expect(page.locator(".file-preview pre")).toContainText(
      "E2E Beta file evidence",
    );
    await expect(page.locator(".file-preview pre")).not.toContainText("Alpha");
    page.on("dialog", (d) => d.accept());
    await page.locator("input[type=file]").setInputFiles({
      name: "browser-upload.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("browser transfer evidence"),
    });
    await expect(page.getByRole("status")).toContainText("上传任务已受理");
    await page.getByRole("button", { name: "任务", exact: true }).click();
    await expect(
      page.locator(".task-row").filter({ hasText: "file.upload" }).first(),
    ).toContainText("成功", { timeout: 15000 });
    await page
      .getByRole("button", { name: "E2E Beta", exact: true })
      .last()
      .click();
    await page.getByRole("button", { name: /browser-upload.txt/ }).click();
    await expect(page.locator(".file-preview pre")).toContainText(
      "browser transfer evidence",
    );
    await page.getByRole("button", { name: "下载", exact: true }).click();
    await page.getByRole("button", { name: "任务", exact: true }).click();
    const download = page
      .locator(".task-row")
      .filter({ hasText: "file.download" })
      .first();
    await expect(download).toContainText("成功", { timeout: 15000 });
    const event = page.waitForEvent("download");
    await download.getByRole("link", { name: "领取下载文件" }).click();
    const file = await event;
    const path = await file.path();
    expect(readFileSync(path!, "utf8")).toBe("browser transfer evidence");
  });
  test("Git project registration and actual dirty branch evidence", async ({
    page,
  }) => {
    await page.getByRole("tab", { name: "项目与 Git" }).click();
    await page.getByLabel("项目名称").fill("UI checkout");
    await page.getByLabel("项目路径").fill(".");
    await page.getByRole("button", { name: "登记项目" }).click();
    await page.getByRole("button", { name: "UI checkout ." }).click();
    await expect(page.locator(".result-panel")).toContainText(
      "# branch.head main",
    );
    await expect(page.locator(".result-panel")).toContainText("? dirty.txt");
  });
  test("real Docker logs and restart task", async ({ page }) => {
    test.skip(!fixture.dockerID, "Docker not configured");
    await page.getByRole("tab", { name: "Docker", exact: true }).click();
    const row = page
      .locator(".container-row")
      .filter({ hasText: fixture.dockerID.slice(0, 12) });
    await expect(row).toBeVisible();
    await row.getByRole("button", { name: "日志", exact: true }).click();
    await expect(page.locator(".result-panel pre")).toContainText(
      "E2E_DOCKER_LOG",
    );
    page.on("dialog", (d) => d.accept());
    await row.getByRole("button", { name: "重启", exact: true }).click();
    await page.getByRole("button", { name: "任务", exact: true }).click();
    await expect(
      page.locator(".task-row").filter({ hasText: "docker.restart" }).first(),
    ).toContainText("成功", { timeout: 20000 });
  });
  test("PTY accepts input, keeps original host when selection changes, and AI remains honest", async ({
    page,
  }) => {
    page.on("dialog", (d) => d.accept());
    let terminalOutput = "";
    page.on("websocket", (ws) =>
      ws.on("framereceived", (frame) => {
        const message = JSON.parse(String(frame.payload));
        if (message.data?.bytes)
          terminalOutput += Buffer.from(message.data.bytes, "base64").toString(
            "utf8",
          );
      }),
    );
    await page.getByRole("button", { name: "终端", exact: true }).click();
    await expect(page.locator(".terminal-identity")).toContainText("已连接");
    await page.locator(".xterm-helper-textarea").fill("");
    await page
      .locator(".xterm-helper-textarea")
      .pressSequentially("printf '%s%s\\n' UI_PTY_ EVIDENCE");
    await page.locator(".xterm-helper-textarea").press("Enter");
    await expect.poll(() => terminalOutput).toContain("UI_PTY_EVIDENCE");
    await expect(page.locator(".xterm-screen")).toBeVisible();
    await page
      .getByRole("button", { name: "E2E Beta", exact: true })
      .last()
      .click();
    await expect(page.locator(".terminal-identity")).toContainText("E2E Alpha");
    await page.getByRole("button", { name: "收起", exact: true }).click();
    await page.getByRole("button", { name: "分析状态", exact: true }).click();
    await expect(page.locator(".ai-panel")).toContainText("真实 AI 验收未执行");
    await expect(page.getByRole("button", { name: "开始分析" })).toBeDisabled();
  });
  test("revoked hosts remain reviewable in settings before explicit re-enrollment", async ({
    page,
  }) => {
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await page.getByLabel("新主机名称").fill("Recovery review");
    await page.getByRole("button", { name: "创建接入令牌" }).click();
    await page.getByRole("button", { name: "隐藏令牌" }).click();
    const row = page.locator(".settings-host").filter({
      has: page.getByRole("heading", {
        name: "Recovery review",
        exact: true,
      }),
    });
    page.once("dialog", (dialog) => dialog.accept());
    await row.getByRole("button", { name: "撤销身份" }).click();
    await expect(row).toContainText("身份已撤销");
    expect(
      (await (await page.request.get(fixture.base + "/api/hosts")).json()).some(
        (h: any) => h.name === "Recovery review",
      ),
    ).toBe(false);
    page.once("dialog", (dialog) => dialog.accept());
    await row.getByRole("button", { name: "重新绑定" }).click();
    await expect(page.locator(".secret-display")).toBeVisible();
    await page.getByRole("button", { name: "隐藏令牌" }).click();
    await expect(row).not.toContainText("身份已撤销");
    page.once("dialog", (dialog) => dialog.accept());
    await row.getByRole("button", { name: "撤销身份" }).click();
    await expect(row).toContainText("身份已撤销");
    await row.screenshot({ path: "test-results/recovery-settings.png" });
  });
});
