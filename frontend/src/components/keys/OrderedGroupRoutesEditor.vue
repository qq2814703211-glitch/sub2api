<template>
  <fieldset class="space-y-2" :disabled="disabled" data-test="ordered-key-routes">
    <legend class="input-label">{{ t('keys.routes.title') }}</legend>
    <p class="text-xs text-gray-500">{{ t('keys.routes.hint') }}</p>
    <p v-if="!modelValue.length" class="text-sm text-gray-500">{{ t('keys.routes.empty') }}</p>
    <ol class="space-y-2">
      <li v-for="(id, index) in modelValue" :key="id" class="flex items-center gap-2 rounded-lg border border-gray-200 p-2 text-sm dark:border-dark-600" :data-route-id="id">
        <span class="min-w-0 flex-1 truncate">{{ index + 1 }}. {{ options.find(group => group.value === id)?.label ?? `#${id}` }}<span v-if="index === 0" class="ml-2 text-xs text-gray-500">{{ t('keys.routes.primary') }}</span></span>
        <button type="button" :disabled="index === 0" :aria-label="t('keys.routes.up')" @click="move(index, -1)">↑</button>
        <button type="button" :disabled="index === modelValue.length - 1" :aria-label="t('keys.routes.down')" @click="move(index, 1)">↓</button>
        <button type="button" :aria-label="t('keys.routes.remove')" @click="remove(id)">×</button>
      </li>
    </ol>
    <select class="input" :value="''" :aria-label="t('keys.routes.add')" :disabled="disabled || !available.length" data-test="add-route" @change="add">
      <option value="" disabled>{{ available.length ? t('keys.routes.add') : t('keys.routes.noMore') }}</option>
      <option v-for="group in available" :key="group.value" :value="group.value">{{ group.label }}</option>
    </select>
  </fieldset>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
const props = defineProps<{ modelValue: number[]; options: Array<{ value: number; label: string }>; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: number[]] }>()
const { t } = useI18n()
const available = computed(() => props.options.filter(group => !props.modelValue.includes(group.value)))
function move(index: number, delta: number) {
  const next = index + delta
  if (props.disabled || next < 0 || next >= props.modelValue.length) return
  const ids = [...props.modelValue]
  ;[ids[index], ids[next]] = [ids[next], ids[index]]
  emit('update:modelValue', ids)
}
function remove(id: number) { if (!props.disabled) emit('update:modelValue', props.modelValue.filter(value => value !== id)) }
function add(event: Event) {
  const target = event.target as HTMLSelectElement
  const id = Number(target.value)
  if (!props.disabled && available.value.some(group => group.value === id)) emit('update:modelValue', [...props.modelValue, id])
  target.value = ''
}
</script>
