class AddReflectorOptionsToBridges < ActiveRecord::Migration[8.1]
  def change
    add_column :bridges, :jitter_buffer_delay, :integer
    add_column :bridges, :monitor_tgs, :string
    add_column :bridges, :tg_select_timeout, :integer
    add_column :bridges, :mute_first_tx_loc, :boolean
    add_column :bridges, :mute_first_tx_rem, :boolean
    add_column :bridges, :verbose, :boolean
    add_column :bridges, :udp_heartbeat_interval, :integer
    add_column :bridges, :cert_subj_c, :string
    add_column :bridges, :cert_subj_o, :string
    add_column :bridges, :cert_subj_ou, :string
    add_column :bridges, :cert_subj_l, :string
    add_column :bridges, :cert_subj_st, :string
    add_column :bridges, :cert_subj_gn, :string
    add_column :bridges, :cert_subj_sn, :string
    add_column :bridges, :cert_email, :string
  end
end
