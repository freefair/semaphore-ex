module.exports = (api) => ({
  presets: [
    ['@vue/cli-plugin-babel/preset', {
      // Mocha bundles Vue templates with Webpack, which needs their named exports.
      exclude: api.env('test') ? [
        '@babel/plugin-transform-modules-commonjs',
        '@babel/plugin-transform-dynamic-import',
      ] : [],
    }],
  ],
});
