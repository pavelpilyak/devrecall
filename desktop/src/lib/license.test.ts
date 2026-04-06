import { describe, it, expect } from "vitest";
import { get } from "svelte/store";
import {
  licenseInfo,
  currentPlan,
  isPro,
  hasFeature,
  isProFeature,
  featureLabel,
  FREE_LICENSE,
  type LicenseInfo,
} from "./license";

describe("licenseInfo store", () => {
  it("defaults to free plan", () => {
    licenseInfo.set(FREE_LICENSE);
    expect(get(licenseInfo).plan).toBe("free");
    expect(get(licenseInfo).features).toEqual([]);
  });

  it("currentPlan derives from licenseInfo", () => {
    licenseInfo.set(FREE_LICENSE);
    expect(get(currentPlan)).toBe("free");

    licenseInfo.set({ plan: "pro", features: ["chat"], devices_used: 1, devices_allowed: 1 });
    expect(get(currentPlan)).toBe("pro");
  });

  it("isPro is true for pro and team plans", () => {
    licenseInfo.set(FREE_LICENSE);
    expect(get(isPro)).toBe(false);

    licenseInfo.set({ plan: "pro", features: ["chat"], devices_used: 1, devices_allowed: 1 });
    expect(get(isPro)).toBe(true);

    licenseInfo.set({ plan: "team", features: ["chat"], devices_used: 1, devices_allowed: 1 });
    expect(get(isPro)).toBe(true);
  });
});

describe("hasFeature", () => {
  it("returns false for free plan", () => {
    expect(hasFeature(FREE_LICENSE, "chat")).toBe(false);
  });

  it("returns true when feature is in list", () => {
    const info: LicenseInfo = { plan: "pro", features: ["chat", "slack"], devices_used: 1, devices_allowed: 1 };
    expect(hasFeature(info, "chat")).toBe(true);
    expect(hasFeature(info, "slack")).toBe(true);
  });

  it("returns false when feature is not in list", () => {
    const info: LicenseInfo = { plan: "pro", features: ["chat"], devices_used: 1, devices_allowed: 1 };
    expect(hasFeature(info, "slack")).toBe(false);
  });
});

describe("isProFeature", () => {
  it("returns true for pro features", () => {
    expect(isProFeature("slack")).toBe(true);
    expect(isProFeature("chat")).toBe(true);
    expect(isProFeature("brag")).toBe(true);
    expect(isProFeature("backup")).toBe(true);
  });
});

describe("featureLabel", () => {
  it("returns human-readable labels", () => {
    expect(featureLabel("slack")).toBe("Slack integration");
    expect(featureLabel("llm")).toBe("AI-powered standups");
    expect(featureLabel("chat")).toBe("AI chat");
  });
});
