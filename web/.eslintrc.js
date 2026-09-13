module.exports = {
  root: true,

  env: {
    node: true,
  },

  extends: [
    'plugin:import/recommended',
    'plugin:vue/essential',
    '@vue/airbnb',
  ],

  parserOptions: {
    parser: 'babel-eslint',
  },

  rules: {
    'no-bitwise': 'off',
    'no-console': process.env.NODE_ENV === 'production' ? 'warn' : 'off',
    'no-debugger': process.env.NODE_ENV === 'production' ? 'warn' : 'off',
    'linebreak-style': 'off',
    'prefer-destructuring': 'off',
    'vuejs-accessibility/click-events-have-key-events': 'off',
    'vuejs-accessibility/no-autofocus': 'off',
    'vue/valid-v-slot': 'off',
    'vue/multi-word-component-names': 'off',
  },

  overrides: [
    {
      files: [
        '**/__tests__/*.{j,t}s?(x)',
        '**/tests/unit/**/*.spec.{j,t}s?(x)',
      ],
      env: {
        mocha: true,
      },
    },
    {
      files: ['gulpfile.js', 'gulp-*.js'],
      rules: {
        'import/no-extraneous-dependencies': ['error', { devDependencies: true }],
        // Gulp plugins mutate the vinyl file they receive; that is the plugin contract.
        'no-param-reassign': ['error', { props: true, ignorePropertyModificationsFor: ['file'] }],
      },
    },
  ],

  settings: {
    'import/resolver': {
      node: {
        extensions: ['.js', '.vue'],
      },
      alias: {
        map: ['@', './src'],
        extensions: ['.vue', '.js'],
      },
    },
  },
};
