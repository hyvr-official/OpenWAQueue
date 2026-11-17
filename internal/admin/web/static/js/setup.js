$(function () {
    var $alert = $("#alert");

    function showError(text) {
        $alert.removeClass("d-none").text(text);
    }

    $("#setup-form").on("submit", function (e) {
        e.preventDefault();
        var data = {};
        $.each($(this).serializeArray(), function (_, field) {
            data[field.name] = field.value;
        });
        $.post("/admin/setup", data).done(function () {
            window.location.href = "/";
        }).fail(function (xhr) {
            if (xhr.status === 401) {
                window.location.href = "/login";
                return;
            }
            showError(xhr.responseJSON && xhr.responseJSON.error ? xhr.responseJSON.error : "Failed to save.");
        });
    });
});
