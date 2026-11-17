window.OWP = (function () {
    var timezone = "UTC";
    var timeFormat = "12";
    var loading = null;

    function loadSettings() {
        if (!loading) {
            loading = $.getJSON("/admin/settings").then(function (data) {
                timezone = data.timezone || "UTC";
                timeFormat = data.time_format === "24" ? "24" : "12";
                return data;
            }, function (xhr) {
                handleError(xhr);
                return $.Deferred().resolve({ timezone: timezone, time_format: timeFormat }).promise();
            });
        }
        return loading;
    }

    function setSettings(data) {
        timezone = data.timezone;
        timeFormat = data.time_format === "24" ? "24" : "12";
        loading = $.Deferred().resolve(data).promise();
    }

    function formatDate(date, tz, format) {
        var fmt = format || timeFormat;
        try {
            return new Intl.DateTimeFormat("en-GB", {
                timeZone: tz || timezone,
                day: "2-digit",
                month: "short",
                year: "numeric",
                hour: "2-digit",
                minute: "2-digit",
                second: "2-digit",
                hour12: fmt !== "24"
            }).format(date);
        } catch (e) {
            return date.toISOString();
        }
    }

    function fmtTime(ts) {
        return ts ? formatDate(new Date(ts * 1000)) : "-";
    }

    function handleError(xhr) {
        if (xhr && xhr.status === 401) {
            window.location.href = "/login";
        }
    }

    return {
        loadSettings: loadSettings,
        setSettings: setSettings,
        formatDate: formatDate,
        fmtTime: fmtTime,
        handleError: handleError
    };
})();
