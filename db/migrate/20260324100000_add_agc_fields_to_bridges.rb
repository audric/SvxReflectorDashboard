class AddAgcFieldsToBridges < ActiveRecord::Migration[8.0]
  def change
    add_column :bridges, :agc_target_level, :float
    add_column :bridges, :agc_attack_rate, :float
    add_column :bridges, :agc_decay_rate, :float
    add_column :bridges, :agc_max_gain, :float
    add_column :bridges, :agc_min_gain, :float
    add_column :bridges, :agc_limit_level, :float
  end
end
