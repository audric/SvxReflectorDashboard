class AddFilterFieldsToBridges < ActiveRecord::Migration[8.0]
  def change
    add_column :bridges, :filter_hpf_cutoff, :float
    add_column :bridges, :filter_lpf_cutoff, :float
  end
end
