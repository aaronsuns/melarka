import { parseYouTubeLink, playlistUrl, watchUrl } from "./youtubeLink";

test("a watch link with a list carries both ids", () => {
  expect(parseYouTubeLink("https://www.youtube.com/watch?v=IiFm7AWP9n4&list=PL1F3EC94FCEA4669F")).toEqual({
    videoId: "IiFm7AWP9n4",
    listId: "PL1F3EC94FCEA4669F",
    isMix: false,
  });
});

test("a list=RD… link is a mix", () => {
  expect(parseYouTubeLink("https://youtu.be/IiFm7AWP9n4?list=RDIiFm7AWP9n4")).toEqual({
    videoId: "IiFm7AWP9n4",
    listId: "RDIiFm7AWP9n4",
    isMix: true,
  });
});

test("a playlist-only link has no video id", () => {
  expect(parseYouTubeLink("https://music.youtube.com/playlist?list=OLAK5uy_kmPRjHDECIcuVwnKsx2Ng7fyNgFK")).toEqual({
    videoId: null,
    listId: "OLAK5uy_kmPRjHDECIcuVwnKsx2Ng7fyNgFK",
    isMix: false,
  });
});

test("shorts and youtu.be paths give a video id", () => {
  expect(parseYouTubeLink("https://www.youtube.com/shorts/IiFm7AWP9n4")?.videoId).toBe("IiFm7AWP9n4");
  expect(parseYouTubeLink("http://youtu.be/IiFm7AWP9n4")?.videoId).toBe("IiFm7AWP9n4");
});

test("anything else is not a link", () => {
  expect(parseYouTubeLink("https://evil.com/watch?v=IiFm7AWP9n4")).toBeNull();
  expect(parseYouTubeLink("邓丽君")).toBeNull();
  expect(parseYouTubeLink("youtube.com/watch?v=IiFm7AWP9n4")).toBeNull();
  expect(parseYouTubeLink("https://www.youtube.com/watch?v=short")).toBeNull();
  expect(parseYouTubeLink("https://www.youtube.com:8443/watch?v=IiFm7AWP9n4")).toBeNull();
  expect(parseYouTubeLink("https://www.youtube.com/playlist?list=RDIiFm7AWP9n4")).toBeNull(); // a mix alone
});

test("canonical urls", () => {
  expect(watchUrl("abc")).toBe("https://www.youtube.com/watch?v=abc");
  expect(playlistUrl("PLx")).toBe("https://www.youtube.com/playlist?list=PLx");
});
