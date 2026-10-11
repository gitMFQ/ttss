import next from "eslint-config-next";
import nextCoreWebVitals from "eslint-config-next/core-web-vitals";
import nextTypescript from "eslint-config-next/typescript";

const eslintConfig = [
    ...nextCoreWebVitals,
    ...nextTypescript,
    ...next,
    {
        // 本项目使用 React 19，但未启用 React Compiler。
        // eslint-plugin-react-hooks@7 的 recommended 默认开启全套 React Compiler 规则，
        // 这些规则假设编译器已启用，会对正常的非编译器代码模式（如 effect 内 setState、
        // render 期读取表单状态）误报。此处仅保留通用且无需编译器的两条经典规则，
        // 关闭其余依赖 React Compiler 的规则。
        rules: {
            "react-hooks/rules-of-hooks": "error",
            "react-hooks/exhaustive-deps": "warn",
            "react-hooks/static-components": "off",
            "react-hooks/use-memo": "off",
            "react-hooks/void-use-memo": "off",
            "react-hooks/preserve-manual-memoization": "off",
            "react-hooks/incompatible-library": "off",
            "react-hooks/immutability": "off",
            "react-hooks/globals": "off",
            "react-hooks/refs": "off",
            "react-hooks/set-state-in-effect": "off",
            "react-hooks/error-boundaries": "off",
            "react-hooks/purity": "off",
            "react-hooks/set-state-in-render": "off",
            "react-hooks/unsupported-syntax": "off",
            "react-hooks/config": "off",
            "react-hooks/gating": "off",
        },
    },
    {
        ignores: [".next/**", "node_modules/**"],
    },
];

export default eslintConfig;
