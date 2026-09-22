---
title: Let users download available files
description: Mount media read-only, map the paths each library reports, and verify a real file from a user's device.
sidebar:
  order: 8
---

Completed-file downloads are optional and start disabled. They let authorized users save files that Radarr, Sonarr, Chaptarr, or Lidarr already indexes. They are separate from the download-client queue.

## Three requirements

1. Cantinarr must be able to read the files on disk.
2. `CANTINARR_MEDIA_ROOTS` must allow the Cantinarr-visible parent directory.
3. The individual instance must have an explicit mapping from its reported path to the Cantinarr-visible path, with downloads enabled.

An arr API reports file paths. It does not give Cantinarr the file bytes through that API. A successful service connection alone cannot enable file downloads.

## Docker example

Add the media mount and allowlist to your Compose service:

```yaml
    volumes:
      - ./config:/config
      - /mnt/nas/media:/media:ro
    environment:
      CANTINARR_MEDIA_ROOTS: /media
```

Use your real host media path. `:ro` makes the mount read-only. Recreate the container after changing mounts or environment variables.

Then open the Radarr instance in Cantinarr and add a mapping:

| Field | Example |
| --- | --- |
| Path reported by Radarr | `/data/media/movies` |
| Path Cantinarr can read | `/media/movies` |

For a Radarr file at `/data/media/movies/Example/file.mkv`, Cantinarr reads `/media/movies/Example/file.mkv`.

## Multiple libraries and operating systems

Add mappings for the exact prefixes reported by each instance. An arr source may use a POSIX path, a Windows drive path, or a UNC path even when Cantinarr runs on Linux.

Chaptarr can need separate mappings for ebook and audiobook storage. Folder names do not determine the book format; the live Chaptarr record does.

The target must stay inside one of the configured media roots. Do not allow `/` to expose the whole filesystem. Multiple roots are comma-separated.

## Access and supported files

Cantinarr rechecks the live file record and the user's access before issuing a short-lived, file-scoped link. Links do not contain arr credentials.

This covers primary indexed movie, episode, book, and music-track files. It does not expose arbitrary files, sidecar subtitles, or extras simply because they sit nearby. Albums are downloaded per track, not repackaged into an archive.

## Verify it

Open an available title as an authorized ordinary user and download one real file. Confirm its name, expected size, and that the device can open it. Test both book formats if their storage differs.

If a download fails, check the live arr file path, mapping, allowed root, mount, and filesystem permissions in that order. Do not make the media mount writable as a workaround. See [file and playback troubleshooting](/troubleshooting/playback/).
