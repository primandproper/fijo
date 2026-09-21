.PHONY: install check test build lint lint-fix format format-check dev

# npm >= 11 is required: npm 10's tree builder fails on vitest 4's optional
# peers ("Cannot read properties of null (reading 'edgesOut')").
install:
	npm ci

check:
	npm run check

test:
	npm run test

build:
	npm run build

lint:
	npm run lint

lint-fix:
	npm run lint:fix

format:
	npm run format

format-check:
	npm run format:check

dev:
	npm run dev
