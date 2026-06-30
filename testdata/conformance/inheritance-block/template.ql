@extends "base.ql"
@block intro {
{{ parent() }}
plus child intro
@}
@block body {
@for item in items {
- {{ item }}
@}
@}
