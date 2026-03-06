class BridgeTgMapping < ApplicationRecord
  belongs_to :bridge
  validates :local_tg, presence: true, numericality: { greater_than: 0 }
  validates :remote_tg, presence: true, numericality: { greater_than: 0 }
end
