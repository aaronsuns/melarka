const files = import.meta.glob(["../**/*.{ts,tsx}", "!../**/*.test.{ts,tsx}", "!../test/**", "!../i18n/**"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

test("no Han characters outside comments in UI source", () => {
  const offenders = Object.entries(files)
    .map(([path, src]) => [path, src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|[^:])\/\/.*$/gm, "$1")] as const)
    .filter(([, code]) => /\p{Script=Han}/u.test(code))
    .map(([path]) => path);
  expect(offenders).toEqual([]);
});
