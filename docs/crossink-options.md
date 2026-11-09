# CrossInk EPUB options

These additions are opt-in. Existing configs retain their previous behavior.
The shared `document.images.screen.width` and `.height` supply both image targets.

```yaml
document:
  empty_line_as_br: true
  annotation:
    include_year: true
  images:
    resize: fit
    baseline_jpeg: true
    screen:
      width: 528
      height: 792
    cover:
      resize: crop
```

- `images.cover.resize: crop`: proportional fill, keeping the full image. One
  side equals the screen; the other is at least screen-sized. The extra pixels
  are NOT cropped or padded. Small covers are enlarged. Pixel rounding can
  introduce an unavoidable subpixel aspect-ratio difference. The reader crops
  the resulting image for its screensaver.
- `images.resize: fit`: non-cover images are downscaled only if they exceed
  the shared screen box. This mode overrides `scale_factor`, so small images
  cannot be accidentally enlarged by a legacy scale setting. `none` (default)
  retains legacy `scale_factor` behavior. Native EPUB SVGs keep vector data;
  their viewport is resized. Rasterized SVGs use the same fit/crop policy.
- `images.baseline_jpeg: true`: decodable non-baseline JPEGs are re-encoded
  through Go's baseline JPEG encoder, even with `optimize: false` and no resize.
  Existing baseline JPEGs are not re-encoded solely for this option. Existing
  format conversion, resize and optimize rules still apply. Encoding uses
  `jpeg_quality_level`. Corrupt files follow the existing `use_broken` policy;
  leaving corrupt originals via `use_broken: true` cannot guarantee baseline.
- `annotation.include_year: true`: in EPUB/Kobo output, append
  `<p class="annotation-year">1962</p>` after the annotation. It uses only
  `description/title-info/date`: the year of a valid `value` takes precedence,
  otherwise a single four-digit year is extracted from its display text.
  Missing, unrecognizable, zero or ambiguous text dates produce no paragraph.
  No document creation date or internet lookup is used. Requires an enabled,
  present annotation. No CSS is added for the year.
- `empty_line_as_br: true`: EPUB/Kobo body flow emits
  `<br class="emptyline"/>` for each FB2 `<empty-line/>`. Consecutive breaks
  remain consecutive. Existing title and inline-footnote breaks remain as-is.
  Keep `.emptyline` margins/padding at zero to avoid extra spacing. The actual
  reader controls page-edge handling; a blank line need not survive a page break.

The `fbc.exe -c config.yaml convert --to epub2 input.fb2 output-directory`
interface used by fb2cng_GUI is unchanged. Select the custom YAML in the GUI.
No changes to the GUI executable are required.
