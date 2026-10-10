# Vendored files

`blaze_face_short_range.tflite` is MediaPipe's BlazeFace short-range face
detector (float16, version 1), Apache License 2.0, from
https://storage.googleapis.com/mediapipe-models/face_detector/blaze_face_short_range/float16/1/blaze_face_short_range.tflite

sha256 `b4578f35940bf5a1a655214a1cce5cab13eba73c1297cd78e1a04c2380b0152f`

It is kept here so a build never depends on that bucket being reachable. The
runner build copies it, with the detector's WebAssembly runtime from
`@mediapipe/tasks-vision`, to `take/assets/vision-<hash>/`; it is fetched only
by a tier 2 attempt (see `src/backend/vision.ts` and docs/10-dpia-camera.md).
