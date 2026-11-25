# Changelog

## [1.1.0](https://github.com/mcsrobert/openbao-rotation-reminders/compare/v1.0.1...v1.1.0) (2026-11-25)


### Features

* **reminder:** default warning lead time to 7d for both deadlines ([590084a](https://github.com/mcsrobert/openbao-rotation-reminders/commit/590084aafd29874563b3d053c9b2846b4fdd9f42))


### Bug Fixes

* **container:** update docker.io/library/golang ( c393c00 ➔ 4309855 ) ([#21](https://github.com/mcsrobert/openbao-rotation-reminders/issues/21)) ([4ab308a](https://github.com/mcsrobert/openbao-rotation-reminders/commit/4ab308aa55d435fe5c58a9dc011dd75f91c390c0))

## [1.0.1](https://github.com/mcsrobert/openbao-rotation-reminders/compare/v1.0.0...v1.0.1) (2026-11-24)


### Bug Fixes

* **app:** retry the OpenBao login and ntfy publish on failed connections ([6af0988](https://github.com/mcsrobert/openbao-rotation-reminders/commit/6af0988c5ef82abca87131ca3b10b8e2ac7161f2))
* **container:** update gcr.io/distroless/static-debian13 ( 25ac8f0 ➔ 8ce6dd8 ) ([#8](https://github.com/mcsrobert/openbao-rotation-reminders/issues/8)) ([4036fd7](https://github.com/mcsrobert/openbao-rotation-reminders/commit/4036fd7d61ffa6e587361baaf31636d52a027696))
* **container:** update go ( 1.27.3 ➔ 1.27.4 ) ([e8ce43e](https://github.com/mcsrobert/openbao-rotation-reminders/commit/e8ce43ea5417af195156482f00cb47118d76ddf8))

## 1.0.0 (2026-11-23)


### Features

* **reminders:** add OpenBao rotation reminders ([b27952c](https://github.com/mcsrobert/openbao-rotation-reminders/commit/b27952c08111e87b28ac973ed891aaf96845ad7c))


### Bug Fixes

* **chart:** fit CronJob names, merge pod labels and quote strings ([683b1f8](https://github.com/mcsrobert/openbao-rotation-reminders/commit/683b1f8ac56c2e5153a9ff825304a85377bca81f))
* **chart:** hash long names and preserve empty pod labels ([ef8c7ee](https://github.com/mcsrobert/openbao-rotation-reminders/commit/ef8c7ee3e7f559e9f042814427c6f765a9e96c6b))
* **config:** reject reserved ntfy topic names ([39c87a4](https://github.com/mcsrobert/openbao-rotation-reminders/commit/39c87a49e3c6b252f97eba51a9edf4c391fa9efe))
* **config:** validate ntfy topics and URL hostnames ([942d202](https://github.com/mcsrobert/openbao-rotation-reminders/commit/942d202da3ab74e73df2da4f77a4b1974072310e))
* **digest:** keep single-finding titles within ntfy's limit ([6a6fd1b](https://github.com/mcsrobert/openbao-rotation-reminders/commit/6a6fd1bb00dab8c962bec3ea4068b78fc50d5902))
* **ntfy:** encode non-ASCII titles and sanitize transport errors ([b448e63](https://github.com/mcsrobert/openbao-rotation-reminders/commit/b448e63044d4459322c5fcc12db8731c28223721))
* **openbao:** fail when the mount is missing or a listing has no keys ([e1f3021](https://github.com/mcsrobert/openbao-rotation-reminders/commit/e1f3021494c6e933406d3f21269c25e6b9aeda16))
* **reminder:** reject out-of-range time zone offsets ([fdd622c](https://github.com/mcsrobert/openbao-rotation-reminders/commit/fdd622c939a9cb40101c1e046d46dea467498674))
