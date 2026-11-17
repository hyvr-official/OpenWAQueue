$(function () {
    var $select = $("#timezone");
    var $format = $("#time-format");
    var $alert = $("#alert");
    var $baseUrl = $("#openwa-base-url");
    var $apiKey = $("#openwa-api-key");
    var $queueKey = $("#queue-api-key");

    function zones() {
        var list = [];
        if (typeof Intl.supportedValuesOf === "function") {
            list = Intl.supportedValuesOf("timeZone").slice();
        }
        if (list.indexOf("UTC") === -1) {
            list.unshift("UTC");
        }
        return list;
    }

    function showAlert(type, text) {
        $alert.removeClass("d-none alert-success alert-danger").addClass("alert-" + type).text(text);
    }

    function updatePreview() {
        var tz = $select.val();
        if (!tz) {
            return;
        }
        $("#preview").text(OWP.formatDate(new Date(), tz, $format.val()));
    }

    OWP.loadSettings().always(function (data) {
        var saved = data.timezone;
        var list = zones();
        if (saved && list.indexOf(saved) === -1) {
            list.unshift(saved);
        }
        $.each(list, function (_, tz) {
            $select.append($("<option>").val(tz).text(tz));
        });
        $select.val(saved);
        $format.val(data.time_format === "24" ? "24" : "12");
        $baseUrl.val(data.openwa_base_url || "");
        $apiKey.val(data.openwa_api_key || "");
        $queueKey.val(data.queue_api_key || "");
        $select.select2({
            theme: "bootstrap-5",
            width: "100%",
            minimumResultsForSearch: 0
        });
        $select.on("change", updatePreview);
        $format.on("change", updatePreview);
        $(document).on("select2:open", function () {
            var field = document.querySelector(".select2-container--open .select2-search__field");
            if (field) {
                field.setAttribute("autocomplete", "off");
                field.focus();
            }
        });
        updatePreview();
        setInterval(updatePreview, 1000);
    });

    function copyField($field) {
        var value = $field.val();
        if (navigator.clipboard && value) {
            navigator.clipboard.writeText(value).then(function () {
                showAlert("success", "API key copied.");
            });
        } else {
            $field.trigger("select");
            document.execCommand("copy");
            showAlert("success", "API key copied.");
        }
    }

    $("#copy-queue-key").on("click", function () {
        copyField($queueKey);
    });

    $("#copy-openwa-key").on("click", function () {
        copyField($apiKey);
    });

    $("#regenerate-queue-key").on("click", function () {
        if (!window.confirm("Regenerate the queue API key? The current key will stop working immediately.")) {
            return;
        }
        $.post("/admin/settings/regenerate-key").done(function (data) {
            $queueKey.val(data.queue_api_key || "");
            showAlert("success", "API key regenerated.");
        }).fail(function (xhr) {
            OWP.handleError(xhr);
            showAlert("danger", "Failed to regenerate API key.");
        });
    });

    $("#settings-form").on("submit", function (e) {
        e.preventDefault();
        $.post("/admin/settings", {
            timezone: $select.val(),
            time_format: $format.val(),
            openwa_base_url: $baseUrl.val(),
            openwa_api_key: $apiKey.val()
        }).done(function (data) {
            OWP.setSettings(data);
            showAlert("success", "Settings saved.");
        }).fail(function (xhr) {
            OWP.handleError(xhr);
            var msg = xhr.responseJSON && xhr.responseJSON.error ? xhr.responseJSON.error : "Failed to save.";
            showAlert("danger", msg);
        });
    });
});
