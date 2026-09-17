export interface AssetGroup {
  groupId: string
  groupType: 'AIGC' | 'LivenessFace'
  groupName: string
  description?: string
  createdTime?: string
  updatedTime?: string
  assetCount?: number
}

export interface Asset {
  assetId: string
  groupId: string
  assetName: string
  assetType: 'Image' | 'Video' | 'Audio'
  assetUrl?: string
  status?: string
  createdTime?: string
}

export interface H5SessionResponse {
  bytedToken: string
  h5Link: string
  expiresIn: number
}
