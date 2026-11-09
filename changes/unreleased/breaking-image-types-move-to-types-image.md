### Changed

- **Breaking: the image records move to `types/image` and lose their prefix.**
  `types.ImageSpec` is `image.Spec`, `types.ImageArchitecture` is `image.Architecture`, and
  so on for all 24. Buzz names are unchanged (`image\Spec`), and so are the JSON shapes.
