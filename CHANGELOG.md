# Changelog

## [0.1.16](https://github.com/lukashankeln/glint/compare/v0.1.15...v0.1.16) (2026-06-17)


### Bug Fixes

* **deps:** update kube-the-home/github-actions action to v1.4.1 ([d740b5d](https://github.com/lukashankeln/glint/commit/d740b5dfbcb3ca79cbe51a28b73a9b6d32e82de1))
* **render:** apply configured helm api_versions ([2a8a926](https://github.com/lukashankeln/glint/commit/2a8a926ee8da58b83f3a08ba600c832dcb97215c))

## [0.1.15](https://github.com/lukashankeln/glint/compare/v0.1.14...v0.1.15) (2026-06-17)


### Features

* use docker based action to improve execution speed ([11e0a14](https://github.com/lukashankeln/glint/commit/11e0a143412c66448b59e261825c39c7bd23c832)), closes [#26](https://github.com/lukashankeln/glint/issues/26)


### Bug Fixes

* remove release type and only use config file ([5b6c5e3](https://github.com/lukashankeln/glint/commit/5b6c5e3336ed3f5000cb992a4641e71b97580598))
* set release type to golang ([adb5dac](https://github.com/lukashankeln/glint/commit/adb5dac7c56776e5ed00faf6666e62c1ae3de965))
* try generic version replace for image tag ([72d7d5a](https://github.com/lukashankeln/glint/commit/72d7d5a68b261c66dd34d02164965905efa61bb6))

## [0.1.14](https://github.com/lukashankeln/glint/compare/v0.1.13...v0.1.14) (2026-06-17)


### Bug Fixes

* release please permissions ([c8a0618](https://github.com/lukashankeln/glint/commit/c8a06186c916f24a61c42dd26f725f27d1b17340))
* use shared variant of release please ([85b9ed9](https://github.com/lukashankeln/glint/commit/85b9ed9367ee1e866421dba68145283449eb2bbf))

## [0.1.13](https://github.com/lukashankeln/glint/compare/v0.1.12...v0.1.13) (2026-06-17)


### Features

* adding dockerfile and build action ([83931aa](https://github.com/lukashankeln/glint/commit/83931aa88f80b170734657a0a02366a098823193)), closes [#25](https://github.com/lukashankeln/glint/issues/25)


### Bug Fixes

* **deps:** update module helm.sh/helm/v4 to v4.2.1 ([137abd3](https://github.com/lukashankeln/glint/commit/137abd342295774510c4a19b48e476ff5bc3b2dc))

## [0.1.12](https://github.com/lukashankeln/glint/compare/v0.1.11...v0.1.12) (2026-06-05)


### Bug Fixes

* github action should use go 1.26 as defualt ([f1d2ec6](https://github.com/lukashankeln/glint/commit/f1d2ec6063b4d20d52298bb4cc718b43b30985b7))

## [0.1.11](https://github.com/lukashankeln/glint/compare/v0.1.10...v0.1.11) (2026-06-05)


### Features

* migrate to helm v4 ([e26dedc](https://github.com/lukashankeln/glint/commit/e26dedc02cc9e18e254ec94fd9865885cdf48952))

## [0.1.10](https://github.com/lukashankeln/glint/compare/v0.1.9...v0.1.10) (2026-06-04)


### Features

* **deps:** update module helm.sh/helm/v3 to v3.21.0 ([471a2c8](https://github.com/lukashankeln/glint/commit/471a2c8f69730a5be8c2c07513eaf3d6d446b378))

## [0.1.9](https://github.com/lukashankeln/glint/compare/v0.1.8...v0.1.9) (2026-06-03)


### Bug Fixes

* apply go fix results ([b412b02](https://github.com/lukashankeln/glint/commit/b412b02eb5956562c152769de11d03f8d5e96567))

## [0.1.8](https://github.com/lukashankeln/glint/compare/v0.1.7...v0.1.8) (2026-05-26)


### Bug Fixes

* improve error logging for github ([3befba5](https://github.com/lukashankeln/glint/commit/3befba5c8d4cee8ed7c94a4d1de8e196ad2ff2f3))
* upgrade github action dependencies and pin versions ([efc23f3](https://github.com/lukashankeln/glint/commit/efc23f3b29da17fc8be3cf4eb1c6f89c5fae11fc))

## [0.1.7](https://github.com/lukashankeln/glint/compare/v0.1.6...v0.1.7) (2026-05-23)


### Bug Fixes

* add explicit cache save and restore steps ([cf21abe](https://github.com/lukashankeln/glint/commit/cf21abe9fe979ff229910cc6f1a5202455f2c05c))
* another try of fixing the caching of the installed version ([9e3f443](https://github.com/lukashankeln/glint/commit/9e3f4431f51aa35145ec952b46dd3de6cb2771a6))

## [0.1.6](https://github.com/lukashankeln/glint/compare/v0.1.5...v0.1.6) (2026-05-23)


### Bug Fixes

* issues with caching in github action ([72295f2](https://github.com/lukashankeln/glint/commit/72295f2fdd6781981ed47610c01a58aace9dc23a))

## [0.1.5](https://github.com/lukashankeln/glint/compare/v0.1.4...v0.1.5) (2026-05-23)


### Bug Fixes

* missing v in default version input ([8b61624](https://github.com/lukashankeln/glint/commit/8b616241c5a9ca1d69c5f3e873d78924ceeb9621))

## [0.1.4](https://github.com/lukashankeln/glint/compare/v0.1.3...v0.1.4) (2026-05-23)


### Bug Fixes

* improve discovery performance ([04fb5ac](https://github.com/lukashankeln/glint/commit/04fb5ac5bf8ff1c5f0bbdd8aa57b064f32fe4442))
* pre-render cel rules ([c5173d4](https://github.com/lukashankeln/glint/commit/c5173d4b0658483989cfad9dd5d43e0795b150db))
* remove not needed marshal in rendering ([95b8b6a](https://github.com/lukashankeln/glint/commit/95b8b6ac3deee6e4bdb0107d0ebdec9bdcddd517))
* use release-please to update version in action for caching ([ccc26a5](https://github.com/lukashankeln/glint/commit/ccc26a5905ad9b0bf8762c41fe89d8474337f54f))

## [0.1.3](https://github.com/lukashankeln/glint/compare/v0.1.2...v0.1.3) (2026-05-23)


### Bug Fixes

* remove zerolog and viper as dependencies ([29785ab](https://github.com/lukashankeln/glint/commit/29785ab97cea4a06c9f3237997ffd7c99f6342b6))

## [0.1.2](https://github.com/lukashankeln/glint/compare/v0.1.1...v0.1.2) (2026-05-23)


### Bug Fixes

* improve logging in code as well as action ([858b92c](https://github.com/lukashankeln/glint/commit/858b92c1794d294f10a5f675ab5415917082354e))
* only run glint once in github action if sarif upload is active ([d59e1c1](https://github.com/lukashankeln/glint/commit/d59e1c1507b9831b5d1944fa9c62e90dc571a91e))
* parallelize discovery of files ([9c0345e](https://github.com/lukashankeln/glint/commit/9c0345ef662c5976072f16ce7ff528c798491b9a))
* parallelize rendering of applciations ([fe2ebf2](https://github.com/lukashankeln/glint/commit/fe2ebf244294a2d5f00be683f7d5eac841686ff1))

## [0.1.1](https://github.com/lukashankeln/glint/compare/v0.1.0...v0.1.1) (2026-05-18)


### Features

* initial draft for glint ([f67f1ed](https://github.com/lukashankeln/glint/commit/f67f1ed5063dfa067dfd53973afa036d378da8b7))
