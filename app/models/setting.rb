class Setting < ApplicationRecord
  validates :key, presence: true, uniqueness: true

  def self.get(key, fallback = nil)
    find_by(key: key)&.value || fallback
  end

  def self.set(key, value)
    record = find_or_initialize_by(key: key)
    record.update!(value: value)
  end
end
