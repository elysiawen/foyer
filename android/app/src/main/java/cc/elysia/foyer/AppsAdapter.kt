package cc.elysia.foyer

import android.content.Context
import android.graphics.Color
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.TextView

/** 应用卡片列表适配器。 */
class AppsAdapter(context: Context) : BaseAdapter() {

    private val inflater = LayoutInflater.from(context)
    private val items = mutableListOf<PortalParser.AppItem>()

    fun replace(apps: List<PortalParser.AppItem>) {
        items.clear()
        items.addAll(apps)
        notifyDataSetChanged()
    }

    override fun getCount() = items.size
    override fun getItem(position: Int) = items[position]
    override fun getItemId(position: Int) = position.toLong()

    override fun getView(position: Int, convertView: View?, parent: ViewGroup): View {
        val view = convertView ?: inflater.inflate(R.layout.item_app, parent, false)
        val app = items[position]

        view.findViewById<TextView>(R.id.app_icon).text = app.icon
        view.findViewById<TextView>(R.id.app_name).text = app.name

        val desc = view.findViewById<TextView>(R.id.app_desc)
        desc.visibility = if (app.desc.isEmpty()) View.GONE else View.VISIBLE
        desc.text = app.desc

        val status = view.findViewById<TextView>(R.id.app_status)
        when (app.status) {
            PortalParser.Status.UP -> {
                status.text = "在线"
                status.setTextColor(Color.parseColor("#15803D"))
                status.setBackgroundResource(R.drawable.bg_status_up)
            }
            PortalParser.Status.DOWN -> {
                status.text = "离线"
                status.setTextColor(Color.parseColor("#B91C1C"))
                status.setBackgroundResource(R.drawable.bg_status_down)
            }
            PortalParser.Status.UNKNOWN -> {
                status.text = "检测中"
                status.setTextColor(Color.parseColor("#64748B"))
                status.setBackgroundResource(R.drawable.bg_status_unknown)
            }
        }
        return view
    }
}
