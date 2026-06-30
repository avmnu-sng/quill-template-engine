@for u in users {
{{ u.name }}{{ " (admin)" if u.admin }}{{ " (guest)" if not u.admin }}
@}
trailing{{ "!" if loud }}
suppressed{{ "?" if quiet }}
