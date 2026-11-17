$(function () {
    var page = 1;
    var paused = false;
    var items = {};
    var statusClass = {
        pending: "text-bg-secondary",
        processing: "text-bg-info",
        done: "text-bg-success",
        failed: "text-bg-danger"
    };
    var modal = new bootstrap.Modal(document.getElementById("detail"));
    var confirmModal = new bootstrap.Modal(document.getElementById("confirm"));
    var pendingAction = null;

    var fmtTime = OWP.fmtTime;

    function pretty(text) {
        if (!text) {
            return "-";
        }
        try {
            return JSON.stringify(JSON.parse(text), null, 2);
        } catch (e) {
            return text;
        }
    }

    function renderRows(data) {
        var $rows = $("#rows").empty();
        items = {};
        if (!data.items.length) {
            $rows.append('<tr><td colspan="7" class="text-center text-secondary py-4">Queue is empty</td></tr>');
            return;
        }
        $.each(data.items, function (_, it) {
            items[it.id] = it;
            var path = "/" + it.path + (it.query ? "?" + it.query : "");
            var $tr = $("<tr>");
            $tr.append($("<td>").text(it.id));
            $tr.append($('<td class="d-none d-md-table-cell">').text(fmtTime(it.created_at)));
            $tr.append($('<td class="d-none d-sm-table-cell">').text(it.method));
            $tr.append($('<td class="path">').text(path));
            $tr.append($("<td>").append($('<span class="badge">').addClass(statusClass[it.status] || "text-bg-secondary").text(it.status)));
            $tr.append($('<td class="d-none d-md-table-cell">').text(it.response_status || "-"));
            var $actions = $('<td class="actions">');
            $actions.append($('<button type="button" class="btn btn-outline-secondary btn-sm view">View</button>').attr("data-id", it.id));
            if (it.status === "failed") {
                $actions.append($('<button type="button" class="btn btn-outline-warning btn-sm retry" title="Retry"><i class="ri-restart-line"></i></button>').attr("data-id", it.id));
            }
            if (it.status !== "processing") {
                $actions.append($('<button type="button" class="btn btn-outline-danger btn-sm del" title="Delete"><i class="ri-delete-bin-line"></i></button>').attr("data-id", it.id));
            }
            $tr.append($actions);
            $rows.append($tr);
        });
    }

    function pageItem(label, target, disabled, active) {
        var $li = $('<li class="page-item">').toggleClass("disabled", !!disabled).toggleClass("active", !!active);
        $li.append($('<a class="page-link" href="#">').html(label).attr("data-page", target));
        return $li;
    }

    function renderPager(data) {
        var $p = $("#pager").empty();
        var start = Math.max(1, data.page - 2);
        var end = Math.min(data.pages, data.page + 2);
        $p.append(pageItem(`<i class="ri-arrow-left-s-line"></i>`, data.page - 1, data.page <= 1));
        if (start > 1) {
            $p.append(pageItem("1", 1));
        }
        if (start > 2) {
            $p.append(pageItem("…", data.page, true));
        }
        for (var i = start; i <= end; i++) {
            $p.append(pageItem(String(i), i, false, i === data.page));
        }
        if (end < data.pages - 1) {
            $p.append(pageItem("…", data.page, true));
        }
        if (end < data.pages) {
            $p.append(pageItem(String(data.pages), data.pages));
        }
        $p.append(pageItem(`<i class="ri-arrow-right-s-line"></i>`, data.page + 1, data.page >= data.pages));
    }

    function renderState(data) {
        paused = data.paused;
        var c = data.counts || {};

        $("#c-pending").text((c.pending || 0) + (c.processing || 0));
        $("#c-done").text(c.done || 0);
        $("#c-failed").text(c.failed || 0);
        $("#retry-failed").prop("disabled", !c.failed);
        $("#state").toggleClass("text-bg-success", !paused).toggleClass("text-bg-warning", paused).html(paused ? "Paused" : "Running");
        $("#toggle").toggleClass("btn-warning", !paused).toggleClass("btn-success", paused).html(paused ? `<i class="ri-play-circle-line"></i><span class="d-none d-sm-inline"> Start queue</span>` : `<i class="ri-pause-circle-line"></i><span class="d-none d-sm-inline"> Pause queue</span>`);
    }

    function handleError(xhr) {
        if (xhr.status === 401) {
            window.location.href = "/login";
        }
    }

    function load() {
        $.getJSON("/admin/queue", { page: page }).done(function (data) {
            if (page > data.pages) {
                page = data.pages;
                load();
                return;
            }
            renderRows(data);
            renderPager(data);
        }).fail(handleError);
        $.getJSON("/admin/state").done(renderState).fail(handleError);
    }

    $("#pager").on("click", "a.page-link", function (e) {
        e.preventDefault();
        if ($(this).parent().hasClass("disabled")) {
            return;
        }
        page = parseInt($(this).attr("data-page"), 10);
        load();
    });

    $("#toggle").on("click", function () {
        $.post(paused ? "/admin/resume" : "/admin/pause").done(load).fail(handleError);
    });

    function confirmAction(message, action) {
        pendingAction = action;
        $("#confirm-message").text(message);
        confirmModal.show();
    }

    $("#confirm-ok").on("click", function () {
        confirmModal.hide();
        if (pendingAction) {
            pendingAction();
            pendingAction = null;
        }
    });

    $("#clear").on("click", function () {
        confirmAction("Delete all queue items? Items currently processing are kept.", function () {
            $.post("/admin/queue/clear").done(load).fail(handleError);
        });
    });

    $("#retry-failed").on("click", function () {
        $.post("/admin/queue/retry-failed").done(load).fail(handleError);
    });

    $("#rows").on("click", ".retry", function () {
        var id = $(this).attr("data-id");
        $.post("/admin/queue/" + encodeURIComponent(id) + "/retry").done(load).fail(handleError);
    });

    $("#rows").on("click", ".del", function () {
        var id = $(this).attr("data-id");
        confirmAction("Delete queue item #" + id + "?", function () {
            $.ajax({ url: "/admin/queue/" + encodeURIComponent(id), method: "DELETE" }).done(load).fail(handleError);
        });
    });

    $("#rows").on("click", ".view", function () {
        var it = items[$(this).attr("data-id")];
        if (!it) {
            return;
        }
        $("#detail-title").text("#" + it.id + " " + it.method + " /" + it.path);
        $("#d-body").text(pretty(it.body));
        $("#d-response").text(pretty(it.response_body));
        $("#d-error").text(it.error || "-");
        modal.show();
    });

    OWP.loadSettings().always(function () {
        load();
        setInterval(load, 3000);
    });
});
