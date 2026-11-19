import { bilibiliWatchUrl, parseVideoLink, sourceOfId, watchUrlFor } from "./videoLink";

const BV = "BV1GJ411x7h7";

test("YouTube links keep their rules and say their source", () => {
  expect(parseVideoLink("https://www.youtube.com/watch?v=IiFm7AWP9n4&list=PL1F3EC94FCEA4669F")).toEqual({
    source: "youtube", videoId: "IiFm7AWP9n4", listId: "PL1F3EC94FCEA4669F", listUrl: null, isMix: false,
  });
  expect(parseVideoLink("not a link")).toBeNull();
});

test("a Bilibili video link gives the bare id, part 1", () => {
  for (const u of [`https://www.bilibili.com/video/${BV}`, `https://www.bilibili.com/video/${BV}/?p=2`, `https://m.bilibili.com/video/${BV}?spm_id_from=x`, `http://bilibili.com/video/${BV}`]) {
    expect(parseVideoLink(u)).toEqual({ source: "bilibili", videoId: BV, listId: null, listUrl: null, isMix: false });
  }
});

test("a Bilibili list link gives its url", () => {
  const fav = "https://space.bilibili.com/123/favlist?fid=456";
  expect(parseVideoLink(fav)).toEqual({ source: "bilibili", videoId: null, listId: null, listUrl: fav, isMix: false });
  expect(parseVideoLink("https://www.bilibili.com/medialist/detail/ml456")?.listUrl).toBe("https://www.bilibili.com/medialist/detail/ml456");
});

test("short links, look-alike hosts and ports are not Bilibili links", () => {
  for (const u of ["https://b23.tv/abc", `https://bilibili.com.evil.test/video/${BV}`, `https://www.bilibili.com:8080/video/${BV}`, "https://www.bilibili.com/video/av170001", "https://space.bilibili.com/123"]) {
    expect(parseVideoLink(u)).toBeNull();
  }
});

test("ids say their source and watch page", () => {
  expect(sourceOfId(BV)).toBe("bilibili");
  expect(sourceOfId("IiFm7AWP9n4")).toBe("youtube");
  expect(sourceOfId("BVabcdefghi")).toBe("youtube"); // 11 chars: a YouTube id
  expect(bilibiliWatchUrl(BV)).toBe(`https://www.bilibili.com/video/${BV}`);
  expect(watchUrlFor(BV)).toBe(`https://www.bilibili.com/video/${BV}`);
  expect(watchUrlFor("IiFm7AWP9n4")).toBe("https://www.youtube.com/watch?v=IiFm7AWP9n4");
});
