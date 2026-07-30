import { describe, expect, it } from 'vitest'
import type { Category } from './api'
import { removeCategoryFromTree } from './taxonomy'

const category = (id: number, children: Category[] = []): Category => ({
  id,
  parentId: null,
  name: `category-${id}`,
  sortOrder: 0,
  paperCount: 0,
  children,
})

describe('removeCategoryFromTree', () => {
  it('removes a category and all descendants', () => {
    const categories = [category(1, [category(2, [category(3)])]), category(4)]
    expect(removeCategoryFromTree(categories, 2)).toEqual([category(1), category(4)])
  })

  it('removes a root category without changing siblings', () => {
    const categories = [category(1), category(2)]
    expect(removeCategoryFromTree(categories, 1)).toEqual([category(2)])
  })
})
