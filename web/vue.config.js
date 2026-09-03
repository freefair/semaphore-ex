const path = require('path');

const sourceMapMode = process.env.VUE_APP_SOURCE_MAP_MODE || 'none';

if (!['none', 'hidden'].includes(sourceMapMode)) {
  throw new Error(`Unsupported VUE_APP_SOURCE_MAP_MODE: ${sourceMapMode}`);
}

module.exports = {
  productionSourceMap: sourceMapMode === 'hidden',
  configureWebpack: {
    performance: {
      hints: false,
    },
    devtool: sourceMapMode === 'hidden' ? 'hidden-source-map' : false,
    output: {
      devtoolModuleFilenameTemplate: (info) => {
        const relativePath = path.relative(__dirname, info.absoluteResourcePath).replaceAll(path.sep, '/');
        return `webpack://semaphore/${relativePath}`;
      },
    },
    devServer: {
      historyApiFallback: true,
      proxy: {
        '^/api': {
          target: 'http://localhost:3000',
        },
      },
    },
  },
  chainWebpack: (config) => {
    config.plugin('html')
      .tap((args) => {
        // eslint-disable-next-line no-param-reassign
        args[0].minify = false;
        return args;
      });
  },
  transpileDependencies: [
    'vuetify',
  ],
  publicPath: './',
  outputDir: process.env.VUE_APP_OUTPUT_DIR || '../api/public',
};
