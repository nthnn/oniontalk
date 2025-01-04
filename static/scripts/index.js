/*
 * Copyright (c) 2025 - Nathanne Isip
 * This file is part of OnionTalk.
 * 
 * OnionTalk is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published
 * by the Free Software Foundation, either version 3 of the License,
 * or (at your option) any later version.
 * 
 * OnionTalk is distributed in the hope that it will be useful, but
 * WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with OnionTalk. If not, see <https://www.gnu.org/licenses/>.
 */
$(document).ready(function() {
    let ws;
    let username;
    let room;
    let typingTimeout;
    let encryptionKey;

    function escapeHtml(unsafe) {
        return unsafe
            .replace(/&/g, "&amp;")
            .replace(/</g, "&lt;")
            .replace(/>/g, "&gt;")
            .replace(/"/g, "&quot;")
            .replace(/'/g, "&#039;");
    }

    function validateInput(input, maxLength = 50) {
        return input.length > 0 && 
            input.length <= maxLength && 
            /^[a-zA-Z0-9\-_.]+$/.test(input);
    }

    async function generateRoomKey(roomName, password) {
        return await crypto.subtle.importKey(
            "raw",
            await crypto.subtle.digest(
                "SHA-256",
                new TextEncoder()
                    .encode(roomName + password)
            ),
            { name: "AES-GCM" },
            false,
            ["encrypt", "decrypt"]
        );
    }

    async function encryptMessage(text) {
        const iv = crypto.getRandomValues(new Uint8Array(12));
        const encrypted = await crypto.subtle.encrypt(
            {
                name: "AES-GCM",
                iv: iv
            },
            encryptionKey,
            new TextEncoder().encode(text)
        );

        return {
            encrypted: Array.from(new Uint8Array(encrypted)),
            iv: Array.from(iv)
        };
    }

    async function decryptMessage(encryptedData, iv) {
        try {
            const decrypted = await crypto.subtle.decrypt(
                {
                    name: "AES-GCM",
                    iv: new Uint8Array(iv)
                },
                encryptionKey,
                new Uint8Array(encryptedData)
            );

            return new TextDecoder().decode(decrypted);
        }
        catch(error) {
            return "<span class=\"text-danger\">Unable to decrypt message</span>";
        }
    }

    function showError(message) {
        const escapedMessage = escapeHtml(message);
        $("#errorMessage").html(escapedMessage).show();

        setTimeout(() => {
            $("#errorMessage").hide();
        }, 3000);
    }

    $("#joinBtn").click(async function() {
        username = $("#username").val();
        room = $("#room").val();

        const password = $("#password").val();
        if(!username || !room || !password) {
            showError("Please fill in all fields.");
            return;
        }

        if(!validateInput(username) || !validateInput(room)) {
            showError(
                "Invalid username or room name. Use only letters, " +
                "numbers, hyphens, underscores, and dots."
            );
            return;
        }

        username += "#" + Math.floor(100000 + Math.random() * 900000);
        try {
            encryptionKey = await generateRoomKey(room, password);
            $.ajax({
                url: "/create-room",
                method: "POST",
                contentType: "application/json",
                data: JSON.stringify({
                    name: room,
                    password: password
                }),
                success: function() {
                    connectWebSocket();
                },
                error: function(xhr) {
                    if(xhr.status === 401)
                        showError("Invalid password");
                    else if(xhr.status === 404)
                        showError("Room not found");
                    else showError("Error joining room");
                }
            });
        }
        catch(error) {
            showError("Encryption initialization failed");
        }
    });

    function connectWebSocket() {
        ws = new WebSocket("ws://" + window.location.host + "/ws");
        ws.onopen = function() {
            $("#loginForm").hide();
            $("#chatInterface").show();
            $("#roomName").text(room);

            ws.send(JSON.stringify({
                type: "join",
                username: username,
                room: room
            }));
        };

        ws.onmessage = async(e)=> {
            const msg = JSON.parse(e.data);
            if(msg.type === "typing") {
                if(msg.username !== username) {
                    $("#typingIndicator").text(`${msg.username} is typing...`);
                    setTimeout(() => {
                        $("#typingIndicator").text("");
                    }, 1000);
                }
            }
            else if(msg.type === "message") {
                try {
                    let messageClass = "bg-primary text-white", align = "";
                    const decryptedContent = await decryptMessage(
                        msg.content.encrypted,
                        msg.content.iv
                    );

                    if(msg.username != username)
                        messageClass = "border";
                    else align = "align=\"right\"";

                    $("#messages").append(
                        `<div class=\"d-block\" ${align}><small class=\"text-muted\">${msg.username}</small><div style=\"width: fit-content\" title=\"${new Date()}\"><div class=\"${messageClass} p-2 w-auto\" role="button">${decryptedContent}</div></div></div>`
                    );
                    $("#messages").scrollTop($('#messages')[0].scrollHeight);
                }
                catch(error) { }
            }
        };

        ws.onclose = function() {
            showError("Connection closed");

            $("#loginForm").show();
            $("#chatInterface").hide();
        };
    }

    $("#messageInput").on("input", function() {
        clearTimeout(typingTimeout);

        ws.send(JSON.stringify({
            type: "typing",
            username: username,
            room: room
        }));

        typingTimeout = setTimeout(() => {
            $("#typingIndicator").text("");
        }, 1500);
    });

    $("#sendBtn").click(async function() {
        const content = $("#messageInput").val();
        if(!content)
            return;

        try {
            const encryptedContent = await encryptMessage(content);
            ws.send(JSON.stringify({
                type: "message",
                username: username,
                content: encryptedContent,
                room: room
            }));

            $("#messageInput").val("");
        }
        catch(error) {
            showError("Message encryption failed");
        }
    });

    $("#messageInput").keypress((e)=> {
        if(e.which === 13)
            $("#sendBtn").click();
    });
});
