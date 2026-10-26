// Written by AI on 2026.10.19 by model GPT-6.1 Sol.
// Packaging pins a complete immutable release; deploying the Worker switches it.
const release = /*CLIMATE_RELEASE*/ null;
const wrappedBuckets = new WeakMap();

export function releaseKey(key, configuration = release) {
  if (!configuration) return key;
  if (key.startsWith('v6/climate/') || key === 'v6/named/current.json' || key === 'v6/forecast/current.json' || key.startsWith('runner/')) return `${configuration.root}/${key}`;
  const forecast = `v6/forecast/${configuration.forecastRun}/`;
  if (key.startsWith(forecast)) return `${configuration.root}/${key}`;
  return key;
}

export function releaseStorage(bucket) {
  if (!release) return bucket;
  let wrapped = wrappedBuckets.get(bucket);
  if (!wrapped) {
    wrapped = new Proxy(bucket, {get(target, name) {
      if (name === 'get' || name === 'head') return async (key, ...options) => {
        if (name === 'get' && key === 'v6/forecast/current.json') {
          const current = await target.get(key);
          const latest = current && await current.json();
          if (latest?.run > release.forecastRun) return target.get(key, ...options);
        }
        return target[name](releaseKey(key), ...options);
      };
      const value = Reflect.get(target, name, target);
      return typeof value === 'function' ? value.bind(target) : value;
    }});
    wrappedBuckets.set(bucket, wrapped);
  }
  return wrapped;
}
