import type { Category } from './api'

export const removeCategoryFromTree = (categories: Category[], id: number): Category[] =>
  categories
    .filter((category) => category.id !== id)
    .map((category) => ({
      ...category,
      children: removeCategoryFromTree(category.children ?? [], id),
    }))
