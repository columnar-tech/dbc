import { describe, it, expect } from "vitest";
import { render } from "@testing-library/svelte";
import Badge from "./badge/badge.svelte";
import Button from "./button/button.svelte";
import Skeleton from "./skeleton/skeleton.svelte";

describe("Badge", () => {
  it("renders with data-slot attribute", () => {
    const { container } = render(Badge);
    const el = container.querySelector("[data-slot='badge']");
    expect(el).toBeTruthy();
  });

  it("renders as span by default", () => {
    const { container } = render(Badge);
    const el = container.querySelector("span[data-slot='badge']");
    expect(el).toBeTruthy();
  });

  it("applies default variant classes", () => {
    const { container } = render(Badge);
    const el = container.querySelector("[data-slot='badge']");
    expect(el?.className).toContain("bg-primary");
  });

  it("applies secondary variant", () => {
    const { container } = render(Badge, { props: { variant: "secondary" } });
    const el = container.querySelector("[data-slot='badge']");
    expect(el?.className).toContain("bg-secondary");
  });

  it("applies outline variant", () => {
    const { container } = render(Badge, { props: { variant: "outline" } });
    const el = container.querySelector("[data-slot='badge']");
    expect(el?.className).toContain("border-border");
  });

  it("renders as anchor when href is provided", () => {
    const { container } = render(Badge, { props: { href: "/somewhere" } });
    const el = container.querySelector("a[data-slot='badge']");
    expect(el).toBeTruthy();
    expect((el as HTMLAnchorElement).href).toContain("/somewhere");
  });

  it("applies extra className", () => {
    const { container } = render(Badge, { props: { class: "my-custom-class" } });
    const el = container.querySelector("[data-slot='badge']");
    expect(el?.className).toContain("my-custom-class");
  });
});

describe("Button", () => {
  it("renders as button element by default", () => {
    const { container } = render(Button);
    const el = container.querySelector("button[data-slot='button']");
    expect(el).toBeTruthy();
  });

  it("has default type=button", () => {
    const { container } = render(Button);
    const el = container.querySelector("button[data-slot='button']") as HTMLButtonElement;
    expect(el.type).toBe("button");
  });

  it("applies default variant classes", () => {
    const { container } = render(Button);
    const el = container.querySelector("[data-slot='button']");
    expect(el?.className).toContain("bg-primary");
  });

  it("applies destructive variant", () => {
    const { container } = render(Button, { props: { variant: "destructive" } });
    const el = container.querySelector("[data-slot='button']");
    expect(el?.className).toContain("bg-destructive");
  });

  it("applies disabled state", () => {
    const { container } = render(Button, { props: { disabled: true } });
    const el = container.querySelector("button") as HTMLButtonElement;
    expect(el.disabled).toBe(true);
  });

  it("renders as anchor when href is provided", () => {
    const { container } = render(Button, { props: { href: "/test" } });
    const el = container.querySelector("a[data-slot='button']");
    expect(el).toBeTruthy();
  });
});

describe("Skeleton", () => {
  it("renders with data-slot attribute", () => {
    const { container } = render(Skeleton);
    const el = container.querySelector("[data-slot='skeleton']");
    expect(el).toBeTruthy();
  });

  it("has animate-pulse class", () => {
    const { container } = render(Skeleton);
    const el = container.querySelector("[data-slot='skeleton']");
    expect(el?.className).toContain("animate-pulse");
  });

  it("merges extra className", () => {
    const { container } = render(Skeleton, { props: { class: "h-4 w-full" } });
    const el = container.querySelector("[data-slot='skeleton']");
    expect(el?.className).toContain("h-4");
    expect(el?.className).toContain("w-full");
  });
});
